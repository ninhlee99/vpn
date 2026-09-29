package routing

import (
	"net"
	"strings"
	"testing"
)

// Watch must never remove a route it keeps alive: a delete, even one
// immediately followed by an add, leaks traffic outside the tunnel.
func TestReassertOnlyAdds(t *testing.T) {
	s := &Snapshot{
		DefaultGateway: "192.0.2.1",
		VPNServerIP:    "203.0.113.7",
		hostRouteAdded: true,
		overrideAdded:  true,
		tunIface:       "utun9",
	}
	cmds := s.reassertCommands()
	if len(cmds) != 1+len(ipv4OverrideNets)+len(ipv6RejectNets) {
		t.Fatalf("got %d commands, want every managed route: %q", len(cmds), cmds)
	}
	var joined []string
	for _, c := range cmds {
		if c[1] != "add" {
			t.Fatalf("reassert issued a non-add command: %q", c)
		}
		joined = append(joined, strings.Join(c, " "))
	}
	all := strings.Join(joined, "\n")
	for _, want := range []string{
		"-n add -static -host 203.0.113.7 192.0.2.1",
		"-n add -static -net 0.0.0.0/1 -interface utun9",
		"-n add -static -net 128.0.0.0/1 -interface utun9",
		"-n add -inet6 -net :: -prefixlen 1 ::1 -reject -static",
		"-n add -inet6 -net 8000:: -prefixlen 1 ::1 -reject -static",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
}

func TestReassertNothingBeforeSetup(t *testing.T) {
	if cmds := (&Snapshot{}).reassertCommands(); len(cmds) != 0 {
		t.Fatalf("unconfigured snapshot reasserted %q", cmds)
	}
}

func TestReassertIncludesSplitTunnelHosts(t *testing.T) {
	s := &Snapshot{tunIface: "utun9", tunnelHosts: []string{"10.8.0.1"}}
	cmds := s.reassertCommands()
	if len(cmds) != 1 || strings.Join(cmds[0], " ") != "-n add -static -host 10.8.0.1 -interface utun9" {
		t.Fatalf("got %q", cmds)
	}
}

// The kill switch swaps the tunnel's /1 routes for blackholes and back in one
// step; a delete-then-add anywhere in that path would leak traffic.
func TestBlackholeArgs(t *testing.T) {
	cases := map[string][]string{
		"-n change -net 0.0.0.0/1 127.0.0.1 -blackhole":        ipv4BlackholeChangeArgs("0.0.0.0/1"),
		"-n add -static -net 128.0.0.0/1 127.0.0.1 -blackhole": ipv4BlackholeAddArgs("128.0.0.0/1"),
		"-n change -net 0.0.0.0/1 -interface utun9":            ipv4OverrideChangeArgs("0.0.0.0/1", "utun9"),
	}
	for want, got := range cases {
		if strings.Join(got, " ") != want {
			t.Errorf("got %q want %q", strings.Join(got, " "), want)
		}
	}
}

func TestRestoreKeepingBlackholeSkipsOverrides(t *testing.T) {
	// Nothing to remove and nothing installed: it must not touch the /1 halves,
	// and must not fail — exercised without root because every removal is
	// conditional on state this snapshot never acquired.
	s := &Snapshot{overrideAdded: true}
	if err := s.RestoreKeepingBlackhole(); err != nil {
		t.Fatal(err)
	}
}

func TestP2PInterfaceArgs(t *testing.T) {
	tests := []struct {
		name     string
		iface    string
		local    string
		peer     string
		mtu      int
		wantArgs string
	}{
		{
			name:     "valid peer provided",
			iface:    "utun0",
			local:    "192.168.100.205",
			peer:     "192.168.100.1",
			mtu:      1400,
			wantArgs: "utun0 inet 192.168.100.205 192.168.100.1 netmask 255.255.255.255 mtu 1400 up",
		},
		{
			name:     "peer is empty string",
			iface:    "utun0",
			local:    "192.168.100.205",
			peer:     "",
			mtu:      1400,
			wantArgs: "utun0 inet 192.168.100.205 10.64.64.64 netmask 255.255.255.255 mtu 1400 up",
		},
		{
			name:     "peer is 0.0.0.0",
			iface:    "utun0",
			local:    "192.168.100.205",
			peer:     "0.0.0.0",
			mtu:      1400,
			wantArgs: "utun0 inet 192.168.100.205 10.64.64.64 netmask 255.255.255.255 mtu 1400 up",
		},
		{
			name:     "peer equals local IP",
			iface:    "utun0",
			local:    "192.168.100.205",
			peer:     "192.168.100.205",
			mtu:      1400,
			wantArgs: "utun0 inet 192.168.100.205 10.64.64.64 netmask 255.255.255.255 mtu 1400 up",
		},
		{
			name:     "local is defaultPeerIP and peer empty",
			iface:    "utun0",
			local:    "10.64.64.64",
			peer:     "",
			mtu:      1280,
			wantArgs: "utun0 inet 10.64.64.64 10.64.64.65 netmask 255.255.255.255 mtu 1280 up",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Join(p2pInterfaceArgs(tt.iface, tt.local, tt.peer, tt.mtu), " ")
			if got != tt.wantArgs {
				t.Fatalf("got %q, want %q", got, tt.wantArgs)
			}
		})
	}
}

func TestParseRouteGet(t *testing.T) {
	out := "   route to: default\ndestination: default\n       mask: default\n    gateway: 10.20.200.1\n  interface: en0\n"
	iface, gw := parseRouteGet(out)
	if iface != "en0" || gw != "10.20.200.1" {
		t.Fatalf("got %q %q", iface, gw)
	}
	// An interface-only default (stale tunnel route) has no gateway line.
	iface, gw = parseRouteGet("destination: default\n  interface: utun5\n")
	if iface != "utun5" || gw != "" {
		t.Fatalf("got %q %q", iface, gw)
	}
}

func TestUsableDefault(t *testing.T) {
	for _, c := range []struct {
		iface, gw string
		want      bool
	}{
		{"en0", "192.168.1.1", true},
		{"en0", "link#6", false},
		{"en0", "", false},
		{"utun5", "10.64.64.64", false},
		{"", "192.168.1.1", false},
	} {
		if got := usableDefault(c.iface, c.gw); got != c.want {
			t.Errorf("usableDefault(%q,%q) = %v, want %v", c.iface, c.gw, got, c.want)
		}
	}
}

func TestParseScutilGlobalIPv4(t *testing.T) {
	out := "<dictionary> {\n  PrimaryInterface : en0\n  PrimaryService : ABC\n  Router : 10.20.200.1\n}\n"
	iface, gw := parseScutilGlobalIPv4(out)
	if iface != "en0" || gw != "10.20.200.1" {
		t.Fatalf("got %q %q", iface, gw)
	}
}

func TestParseNetstatDefault(t *testing.T) {
	out := `Destination        Gateway            Flags               Netif Expire
default            link#20            UCSIg               utun5
default            192.168.100.1      UGScg                 en0
0/1                utun5              USc                 utun5
`
	iface, gw := parseNetstatDefault(out)
	if iface != "en0" || gw != "192.168.100.1" {
		t.Fatalf("got %q %q", iface, gw)
	}
	if i, g := parseNetstatDefault("default link#4 UCS en0\n"); i != "" || g != "" {
		t.Fatalf("link# gateway accepted: %q %q", i, g)
	}
}

func TestOverlaps(t *testing.T) {
	_, lan, _ := net.ParseCIDR("192.168.100.0/24")
	if !overlaps([]*net.IPNet{lan}, net.ParseIP("192.168.100.205")) {
		t.Error("VPN address inside the LAN not detected")
	}
	if overlaps([]*net.IPNet{lan}, net.ParseIP("10.64.64.64")) {
		t.Error("address outside the LAN flagged")
	}
	if overlaps(nil, net.ParseIP("192.168.100.1")) {
		t.Error("no networks must never overlap")
	}
}

func TestEnsureDefaultRouteNoGatewayIsNoop(t *testing.T) {
	// repair's zero-value snapshot has no captured gateway: must not touch routes.
	if err := (&Snapshot{}).EnsureDefaultRoute(); err != nil {
		t.Fatal(err)
	}
}
