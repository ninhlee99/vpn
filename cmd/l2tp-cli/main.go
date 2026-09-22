// Command l2tp-cli is a native macOS L2TP/IPsec VPN client: multi-profile,
// multi-account, no Docker/WireGuard/strongSwan/xl2tpd/pppd dependency.
package main

import (
	"os"

	"github.com/ninhlee99/vpn-l2tp/internal/cli"
)

var version = "dev" // set via -ldflags "-X main.version=..."

func main() {
	cli.Version = version
	os.Exit(cli.Run(os.Args[1:]))
}
