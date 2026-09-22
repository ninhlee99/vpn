package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"vpn/internal/config"
	"vpn/internal/engine"
	"vpn/internal/keychain"
	"vpn/internal/privilege"
	"vpn/internal/state"
)

// pathTail: absolute path, not bare "tail" — cmdLogs runs this while
// privilege.Elevate is raised (see below), same PATH-hijack concern as
// routing.go/dnsmgr.go/keychain.go.
const pathTail = "/usr/bin/tail"

func cmdConnect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ExitOnError)
	profileName := fs.String("profile", "", "profile to connect (default: active profile)")
	accountName := fs.String("account", "", "account to use (default: profile's default account)")
	timeout := fs.Duration("timeout", 30*time.Second, "overall connect timeout")
	verbose := fs.Bool("verbose", false, "verbose protocol logging")
	fs.Parse(args)

	// No root check here: engine.Connect elevates internally (see
	// internal/privilege) for exactly the steps that need it, and returns
	// a clear error itself if this process can't (not root, not installed
	// setuid via install.sh).

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
		return fmt.Errorf("no PSK stored for profile %q — run `vpn init` or `vpn profile add`: %w", pName, err)
	}
	password, err := keychain.GetPassword(pName, aName)
	if err != nil {
		return fmt.Errorf("no password stored for account %q — run `vpn account add`: %w", aName, err)
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
	// The log file is 0600, owned by whoever ran `connect` (root, via
	// privilege.Elevate) — a plain unprivileged read would fail with
	// permission denied, so briefly elevate just to read/tail it.
	return privilege.Elevate(func() error {
		if *follow {
			c := exec.Command(pathTail, "-f", logPath)
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
	})
}
