package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/ninhlee99/vpn-l2tp/internal/config"
	"github.com/ninhlee99/vpn-l2tp/internal/engine"
	"github.com/ninhlee99/vpn-l2tp/internal/keychain"
	"github.com/ninhlee99/vpn-l2tp/internal/state"
)

func cmdTest(args []string) error {
	// `test` is the no-side-effects sibling of `connect`: it runs the exact
	// same pre-flight diagnostics connect would, without touching routes,
	// DNS, or opening a tunnel interface.
	return cmdDiagnose(args)
}

func cmdConnect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ExitOnError)
	profileName := fs.String("profile", "", "profile to connect (default: active profile)")
	accountName := fs.String("account", "", "account to use (default: profile's default account)")
	timeout := fs.Duration("timeout", 30*time.Second, "overall connect timeout")
	verbose := fs.Bool("verbose", false, "verbose protocol logging")
	fs.Parse(args)

	if os.Geteuid() != 0 {
		return fmt.Errorf("connect must run as root (opens a utun interface and changes routes/DNS) — try: sudo l2tp-cli connect")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pName, p, err := cfg.Profile(*profileName)
	if err != nil {
		return err
	}
	aName, _, err := p.Account(*accountName)
	if err != nil {
		return err
	}
	psk, err := keychain.GetPSK(pName)
	if err != nil {
		return fmt.Errorf("no PSK stored for profile %q — run `l2tp-cli init` or `l2tp-cli profile add`: %w", pName, err)
	}
	password, err := keychain.GetPassword(pName, aName)
	if err != nil {
		return fmt.Errorf("no password stored for account %q — run `l2tp-cli account add`: %w", aName, err)
	}

	return engine.Connect(engine.Config{
		ProfileName:  pName,
		Server:       p.Server,
		ServerID:     p.ServerID,
		AccountName:  aName,
		Password:     password,
		PSK:          psk,
		IKEProposals: p.IKEProposals,
		ESPProposals: p.ESPProposals,
		MTU:          p.MTU,
		FullTunnel:   p.FullTunnel,
		Timeout:      *timeout,
		Verbose:      *verbose,
	})
}

func cmdDisconnect(args []string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("disconnect must run as root — try: sudo l2tp-cli disconnect")
	}
	return engine.Disconnect()
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "output as JSON")
	fs.Parse(args)

	s, err := state.Load()
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(s)
	}
	fmt.Printf("Phase:   %s\n", s.Phase)
	if s.Profile != "" {
		fmt.Printf("Profile: %s (account %s)\n", s.Profile, s.Account)
	}
	if s.Server != "" {
		fmt.Printf("Server:  %s\n", s.Server)
	}
	if s.TunDevice != "" {
		fmt.Printf("Tunnel:  %s (%s)\n", s.TunDevice, s.LocalIP)
	}
	if s.FailStage != "" {
		fmt.Printf("Last failure: %s (%s)\n", s.FailStage, s.FailDetail)
	}
	fmt.Printf("Updated: %s\n", s.UpdatedAt.Format(time.RFC3339))
	return nil
}

func cmdRepair(args []string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("repair must run as root — try: sudo l2tp-cli repair")
	}
	// repair must work without credentials — it only restores routing/DNS
	// state, it never (re)negotiates the tunnel.
	return engine.Repair()
}

func cmdLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	follow := fs.Bool("f", false, "follow the log file")
	fs.Parse(args)

	logPath := engine.LogPath()
	if _, err := os.Stat(logPath); err != nil {
		return fmt.Errorf("no log file yet at %s (nothing has connected)", logPath)
	}
	if *follow {
		c := exec.Command("tail", "-f", logPath)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		return err
	}
	os.Stdout.Write(data)
	return nil
}
