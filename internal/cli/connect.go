package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"
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

// daemonChildEnv marks a re-exec'd `connect` invocation as the detached
// background process itself, so it runs the real connect logic in place
// instead of re-spawning another daemon (which would otherwise recurse
// forever) — see cmdConnect.
const daemonChildEnv = "VPN_DAEMON_CHILD"

func cmdConnect(args []string) error {
	fs := newFlagSet("connect")
	profileName := fs.String("profile", "", "profile to connect (default: active profile)")
	accountName := fs.String("account", "", "account to use (default: profile's default account)")
	timeout := fs.Duration("timeout", 30*time.Second, "overall connect timeout")
	verbose := fs.Bool("verbose", false, "verbose protocol logging")
	// -d/--daemon are accepted but always on — connect always backgrounds
	// itself now; the flags exist only so old scripts/muscle memory using
	// `vpn connect -d` don't break.
	fs.Bool("d", true, "run in background (default; kept for compatibility)")
	fs.Bool("daemon", true, "alias of -d")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if os.Getenv(daemonChildEnv) == "1" {
		return doConnect(*profileName, *accountName, *timeout, *verbose)
	}
	return spawnDaemon(args, *timeout)
}

// spawnDaemon re-execs this same binary as a detached background process
// (new session via Setsid, stdio pointed at /dev/null so it survives the
// parent's terminal closing) running the real connect logic, then polls
// state until it reaches CONNECTED/FAILED (or timeout) so the caller gets
// immediate feedback instead of a background process starting silently
// with no way to tell whether it actually worked.
func spawnDaemon(args []string, timeout time.Duration) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate own executable to re-exec as a daemon: %w", err)
	}

	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	defer devnull.Close()

	cmd := exec.Command(exe, append([]string{"connect"}, args...)...)
	cmd.Env = append(os.Environ(), daemonChildEnv+"=1")
	cmd.Stdin = devnull
	cmd.Stdout = devnull
	cmd.Stderr = devnull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // detach from this terminal's session
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start background connect: %w", err)
	}
	fmt.Printf("Connecting in the background (pid %d)...\n", cmd.Process.Pid)

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		st, err := state.Load()
		if err != nil {
			continue
		}
		switch st.Phase {
		case state.PhaseConnected:
			fmt.Printf("Connected (local IP %s, device %s).\n", st.LocalIP, st.TunDevice)
			return nil
		case state.PhaseFailed:
			return fmt.Errorf("%s: %s", st.FailStage, st.FailDetail)
		}
	}
	fmt.Println("Still connecting in the background — check `vpn status` or `vpn logs -f`.")
	return nil
}

// doConnect is the actual connect logic, run inside the detached daemon
// process spawned by spawnDaemon (see daemonChildEnv).
func doConnect(profileName, accountName string, timeout time.Duration, verbose bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pName, p, err := cfg.Profile(profileName)
	if err != nil {
		return err
	}
	aName, _, err := p.Account(accountName)
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
		Timeout:      timeout,
		Verbose:      verbose,
	})
}

func cmdDisconnect(args []string) error {
	return engine.Disconnect()
}

func cmdStatus(args []string) error {
	fs := newFlagSet("status")
	asJSON := fs.Bool("json", false, "output as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

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
	fs := newFlagSet("logs")
	follow := fs.Bool("f", false, "follow the log file")
	if err := fs.Parse(args); err != nil {
		return err
	}

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
