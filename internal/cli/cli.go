// Package cli implements l2tp-cli's subcommands.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/ninhlee99/vpn-l2tp/internal/config"
	"github.com/ninhlee99/vpn-l2tp/internal/diagnostics"
	"github.com/ninhlee99/vpn-l2tp/internal/keychain"
	"github.com/ninhlee99/vpn-l2tp/internal/secretinput"
)

// Version is set at build time via -ldflags "-X .../cli.Version=...".
var Version = "dev"

// Run dispatches argv[1:] to the matching subcommand. It returns the process
// exit code rather than calling os.Exit itself, so main stays a one-liner.
func Run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 2
	}

	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "version":
		fmt.Println("l2tp-cli " + Version)
	case "init":
		err = cmdInit(rest)
	case "profile":
		err = cmdProfile(rest)
	case "account":
		err = cmdAccount(rest)
	case "diagnose":
		err = cmdDiagnose(rest)
	case "test":
		err = cmdTest(rest)
	case "connect":
		err = cmdConnect(rest)
	case "disconnect":
		err = cmdDisconnect(rest)
	case "status":
		err = cmdStatus(rest)
	case "repair":
		err = cmdRepair(rest)
	case "logs":
		err = cmdLogs(rest)
	case "-h", "--help", "help":
		printUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		printUsage()
		return 2
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

func printUsage() {
	fmt.Fprint(os.Stderr, `l2tp-cli — native macOS L2TP/IPsec VPN client (multi-profile, multi-account)

Usage:
  l2tp-cli init                          interactive first-time setup
  l2tp-cli profile add <name> --server <host> [--server-id id] [--mtu n] [--full-tunnel]
  l2tp-cli profile list
  l2tp-cli profile use <name>
  l2tp-cli profile remove <name>
  l2tp-cli account add <profile> <account> [--default]
  l2tp-cli account list <profile>
  l2tp-cli account use <profile> <account>
  l2tp-cli diagnose [--profile name] [--server host] [--json]
  l2tp-cli test [--profile name]
  l2tp-cli connect [--profile name] [--account name] [--timeout 30s]
  l2tp-cli disconnect
  l2tp-cli status [--json]
  l2tp-cli repair
  l2tp-cli logs [-f]
  l2tp-cli version
`)
}

// --- init ---

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	profileName := fs.String("profile", "default", "profile name to create")
	server := fs.String("server", "", "VPN server host or IP")
	serverID := fs.String("server-id", "", "expected IKE remote ID (optional, default: accept any)")
	username := fs.String("username", "", "VPN account username")
	psk := fs.String("psk", "", "IPsec pre-shared key (prompted if omitted)")
	password := fs.String("password", "", "VPN account password (prompted if omitted)")
	fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if *server == "" {
		fmt.Print("VPN server (host or IP): ")
		fmt.Scanln(server)
	}
	if *username == "" {
		fmt.Print("Account username: ")
		fmt.Scanln(username)
	}
	if *psk == "" {
		v, err := secretinput.Prompt("IPsec pre-shared key (PSK)")
		if err != nil {
			return err
		}
		*psk = v
	}
	if *password == "" {
		v, err := secretinput.Prompt("Account password")
		if err != nil {
			return err
		}
		*password = v
	}

	p := &config.Profile{Server: *server, ServerID: *serverID, DefaultAccount: *username}
	cfg.AddProfile(*profileName, p)
	p.Accounts[*username] = &config.Account{Username: *username}

	if err := keychain.SetPSK(*profileName, *psk); err != nil {
		return err
	}
	if err := keychain.SetPassword(*profileName, *username, *password); err != nil {
		return err
	}
	if err := cfg.Save(); err != nil {
		return err
	}

	fmt.Printf("Profile %q created (server=%s, account=%s). Run `l2tp-cli diagnose` next.\n", *profileName, *server, *username)
	return nil
}

// --- profile ---

func cmdProfile(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: l2tp-cli profile <add|list|use|remove> ...")
	}
	switch args[0] {
	case "add":
		return cmdProfileAdd(args[1:])
	case "list":
		return cmdProfileList(args[1:])
	case "use":
		return cmdProfileUse(args[1:])
	case "remove":
		return cmdProfileRemove(args[1:])
	default:
		return fmt.Errorf("unknown `profile` subcommand %q", args[0])
	}
}

func cmdProfileAdd(args []string) error {
	fs := flag.NewFlagSet("profile add", flag.ExitOnError)
	server := fs.String("server", "", "VPN server host or IP (required)")
	serverID := fs.String("server-id", "", "expected IKE remote ID")
	mtu := fs.Int("mtu", 1400, "tunnel MTU override")
	fullTunnel := fs.Bool("full-tunnel", true, "route all traffic through the VPN")
	psk := fs.String("psk", "", "IPsec pre-shared key (prompted if omitted)")
	fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: l2tp-cli profile add <name> --server <host>")
	}
	name := fs.Arg(0)
	if *server == "" {
		return fmt.Errorf("--server is required")
	}
	if *psk == "" {
		v, err := secretinput.Prompt("IPsec pre-shared key (PSK)")
		if err != nil {
			return err
		}
		*psk = v
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.AddProfile(name, &config.Profile{
		Server:     *server,
		ServerID:   *serverID,
		MTU:        *mtu,
		FullTunnel: *fullTunnel,
	})
	if err := keychain.SetPSK(name, *psk); err != nil {
		return err
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("Profile %q added (server=%s).\n", name, *server)
	return nil
}

func cmdProfileList(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(cfg.Profiles))
	for n := range cfg.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p := cfg.Profiles[n]
		marker := "  "
		if n == cfg.ActiveProfile {
			marker = "* "
		}
		fmt.Printf("%s%s\tserver=%s\taccounts=%d\tdefault_account=%s\n", marker, n, p.Server, len(p.Accounts), p.DefaultAccount)
	}
	return nil
}

func cmdProfileUse(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: l2tp-cli profile use <name>")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if _, ok := cfg.Profiles[args[0]]; !ok {
		return fmt.Errorf("unknown profile %q", args[0])
	}
	cfg.ActiveProfile = args[0]
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("Active profile: %s\n", args[0])
	return nil
}

func cmdProfileRemove(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: l2tp-cli profile remove <name>")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	p, ok := cfg.Profiles[args[0]]
	if !ok {
		return fmt.Errorf("unknown profile %q", args[0])
	}
	for acct := range p.Accounts {
		_ = keychain.DeletePassword(args[0], acct)
	}
	_ = keychain.DeletePSK(args[0])
	delete(cfg.Profiles, args[0])
	if cfg.ActiveProfile == args[0] {
		cfg.ActiveProfile = ""
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("Profile %q removed.\n", args[0])
	return nil
}

// --- account ---

func cmdAccount(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: l2tp-cli account <add|list|use> ...")
	}
	switch args[0] {
	case "add":
		return cmdAccountAdd(args[1:])
	case "list":
		return cmdAccountList(args[1:])
	case "use":
		return cmdAccountUse(args[1:])
	default:
		return fmt.Errorf("unknown `account` subcommand %q", args[0])
	}
}

func cmdAccountAdd(args []string) error {
	fs := flag.NewFlagSet("account add", flag.ExitOnError)
	makeDefault := fs.Bool("default", false, "make this the profile's default account")
	password := fs.String("password", "", "account password (prompted if omitted)")
	fs.Parse(args)
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: l2tp-cli account add <profile> <username> [--default]")
	}
	profileName, username := fs.Arg(0), fs.Arg(1)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	_, p, err := cfg.Profile(profileName)
	if err != nil {
		return err
	}
	if *password == "" {
		v, err := secretinput.Prompt(fmt.Sprintf("Password for %s@%s", username, profileName))
		if err != nil {
			return err
		}
		*password = v
	}
	if p.Accounts == nil {
		p.Accounts = map[string]*config.Account{}
	}
	p.Accounts[username] = &config.Account{Username: username}
	if *makeDefault || p.DefaultAccount == "" {
		p.DefaultAccount = username
	}
	if err := keychain.SetPassword(profileName, username, *password); err != nil {
		return err
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("Account %q added to profile %q.\n", username, profileName)
	return nil
}

func cmdAccountList(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: l2tp-cli account list <profile>")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	_, p, err := cfg.Profile(args[0])
	if err != nil {
		return err
	}
	names := make([]string, 0, len(p.Accounts))
	for n := range p.Accounts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		marker := "  "
		if n == p.DefaultAccount {
			marker = "* "
		}
		fmt.Printf("%s%s\n", marker, n)
	}
	return nil
}

func cmdAccountUse(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: l2tp-cli account use <profile> <username>")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	_, p, err := cfg.Profile(args[0])
	if err != nil {
		return err
	}
	if _, ok := p.Accounts[args[1]]; !ok {
		return fmt.Errorf("unknown account %q on profile %q", args[1], args[0])
	}
	p.DefaultAccount = args[1]
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("Default account for %q: %s\n", args[0], args[1])
	return nil
}

// --- diagnose ---

func cmdDiagnose(args []string) error {
	fs := flag.NewFlagSet("diagnose", flag.ExitOnError)
	profileName := fs.String("profile", "", "profile to diagnose (default: active profile)")
	server := fs.String("server", "", "diagnose an arbitrary host instead of a saved profile")
	asJSON := fs.Bool("json", false, "output as JSON")
	timeout := fs.Duration("timeout", 20_000_000_000, "overall diagnostic timeout") // 20s
	fs.Parse(args)

	target := *server
	if target == "" {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		_, p, err := cfg.Profile(*profileName)
		if err != nil {
			return err
		}
		target = p.Server
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	report := diagnostics.Run(ctx, target)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	printReport(report)
	return nil
}

func printReport(r *diagnostics.Report) {
	ok := func(b bool) string {
		if b {
			return "[OK]"
		}
		return "[FAIL]"
	}
	fmt.Printf("%s Network      interface=%s ip=%s gateway=%s mtu=%d\n", ok(r.Network.Interface != ""), r.Network.Interface, r.Network.IPv4, r.Network.Gateway, r.Network.MTU)
	fmt.Printf("%s DNS          servers=%v\n", ok(len(r.Network.DNSServers) > 0), r.Network.DNSServers)
	fmt.Printf("%s VPN DNS      server=%s resolved=%v\n", ok(r.Connectivity.DNSOK), r.Connectivity.ServerHost, r.Connectivity.ResolvedIPs)
	if r.Connectivity.PingRTTMillis > 0 {
		fmt.Printf("     ping avg RTT: %.1fms\n", r.Connectivity.PingRTTMillis)
	}
	fmt.Printf("%s UDP/500      (IKE)\n", ok(r.Connectivity.UDP500Reached))
	fmt.Printf("%s UDP/4500     (NAT-T)\n", ok(r.Connectivity.UDP4500Reached))
	fmt.Println("MTU probes:")
	for _, p := range r.MTUProbes {
		fmt.Printf("  %s %d bytes\n", ok(p.OK), p.Size)
	}
	if r.FailureStage != "" {
		fmt.Printf("\nFAIL\nReason:\n  %s\n", r.FailureStage)
	} else {
		fmt.Println("\nAll pre-flight checks passed — ready to attempt `l2tp-cli connect`.")
	}
	if len(r.Errors) > 0 {
		fmt.Println("Errors:")
		for _, e := range r.Errors {
			fmt.Println("  " + e)
		}
	}
}
