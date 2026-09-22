// Command vpn is a native macOS L2TP/IPsec VPN client: multi-profile,
// multi-account, no Docker/WireGuard/strongSwan/xl2tpd/pppd dependency.
package main

import (
	"fmt"
	"os"

	"vpn/internal/cli"
	"vpn/internal/privilege"
)

var (
	version   = "1.0.0" // override at build time via -ldflags "-X main.version=..."
	sourceDir = ""      // set by install.sh via -ldflags "-X main.sourceDir=...": the repo `vpn update` rebuilds from.
	allowedUID = ""     // set by install.sh via -ldflags "-X main.allowedUID=$(id -u)": only this user may run this binary at all.
)

func main() {
	// Absolute first thing: reject any other local user before this
	// setuid-root binary does anything else, privileged or not — see
	// privilege.CheckOwner's doc comment for why.
	if err := privilege.CheckOwner(allowedUID); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	// Must run before any subcommand dispatch: if this binary is installed
	// setuid-root, every subcommand should start unprivileged by default
	// and only the specific operations that need root (see
	// internal/privilege) ever regain it.
	privilege.Drop()

	cli.Version = version
	cli.SourceDir = sourceDir
	cli.AllowedUID = allowedUID
	os.Exit(cli.Run(os.Args[1:]))
}
