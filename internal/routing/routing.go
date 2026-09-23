// Package routing captures the pre-VPN routing state and installs/restores
// the routes needed for a tunnel, on macOS via the `route` and `ifconfig`
// system tools (no netlink equivalent exists on macOS; there is no
// dependency-free way to do this except shelling out to the system route
// command, which every macOS network tool — including Apple's own — does).
package routing

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"vpn/internal/sysbin"
)

// Snapshot is the pre-VPN routing state, captured once before any change so
// Restore can put the machine back exactly as it was.
type Snapshot struct {
	DefaultInterface string
	DefaultGateway   string
	VPNServerIP      string
	hostRouteAdded   bool
	overrideAdded    bool
	tunIface         string
}

// ipv4OverrideNets are the split-default halves that steer all IPv4 traffic
// into the tunnel (see ApplyFullTunnel).
var ipv4OverrideNets = []string{"0.0.0.0/1", "128.0.0.0/1"}

// ipv6RejectNets are the same split-default trick for IPv6, pointed at a
// -reject route instead of the tunnel: the LNS never negotiates IPV6CP, so
// the tunnel cannot carry IPv6 — but without these, any IPv6-capable
// network would keep sending IPv6 traffic straight out the physical
// interface, bypassing the VPN entirely. -reject (ICMP unreachable) rather
// than -blackhole so dual-stack clients fail over to IPv4 immediately
// instead of timing out. Link-local and on-link LAN prefixes stay reachable
// because their routes are more specific than /1, just like the IPv4 LAN.
var ipv6RejectNets = []string{"::", "8000::"}

// Capture reads the current default route. It must be called before any
// other function in this package changes anything.
func Capture() (*Snapshot, error) {
	out, err := exec.Command(sysbin.Route, "-n", "get", "default").Output()
	if err != nil {
		return nil, fmt.Errorf("read default route: %w", err)
	}
	s := &Snapshot{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "interface:"):
			s.DefaultInterface = strings.TrimSpace(strings.TrimPrefix(line, "interface:"))
		case strings.HasPrefix(line, "gateway:"):
			s.DefaultGateway = strings.TrimSpace(strings.TrimPrefix(line, "gateway:"))
		}
	}
	if s.DefaultGateway == "" {
		return nil, fmt.Errorf("could not determine current default gateway")
	}
	return s, nil
}

// ProtectServer adds an explicit host route to the VPN server through the
// original gateway, so the tunnel's own encrypted traffic (the raw
// UDP/500+4500 IKE/ESP socket, wildcard-bound and therefore re-routed by
// the kernel on every send) keeps leaving via the real interface instead
// of recursing into the tunnel once the default route is overridden.
// `-static` matters here, not just documentation: without it, macOS's own
// network reconciliation (IPMonitor) can reap a route it doesn't recognize
// as one of its own the next time it resyncs the routing table against
// network state — which the split-default 0.0.0.0/1 + 128.0.0.0/1 override
// in ApplyFullTunnel below is exactly the kind of unusual state that
// triggers. If this host route disappears, ESP-to-the-server traffic falls
// through to the /1 default, which points into the tunnel interface — a
// route that can't actually reach the server (only the LNS's own inside
// address is reachable point-to-point through it) — producing exactly a
// `sendto: can't assign requested address` death spiral.
func (s *Snapshot) ProtectServer(serverIP string) error {
	s.VPNServerIP = serverIP
	// Idempotent: delete-then-add so re-running connect after a crash
	// doesn't fail on "route already exists".
	_ = exec.Command(sysbin.Route, "-n", "delete", "-host", serverIP).Run()
	cmd := exec.Command(sysbin.Route, s.serverRouteAddArgs()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("add host route to VPN server via %s: %w (%s)", s.DefaultGateway, err, strings.TrimSpace(string(out)))
	}
	s.hostRouteAdded = true
	return nil
}

// ConfigureP2PInterface brings the utun interface up as a point-to-point
// link to the LNS's inside address — `ifconfig <if> <local> <peer> netmask
// 255.255.255.255 mtu <mtu> up`, the standard BSD point-to-point form (no
// ARP on a utun device, so the kernel needs the explicit peer address
// rather than a subnet to know the link is reachable at all; this also
// implicitly installs the host route to peer that ApplyFullTunnel's
// "-interface" routes rely on being resolvable).
func ConfigureP2PInterface(iface, local, peer string, mtu int) error {
	args := []string{iface, "inet", local, peer, "netmask", "255.255.255.255"}
	if mtu > 0 {
		args = append(args, "mtu", fmt.Sprintf("%d", mtu))
	}
	args = append(args, "up")
	if out, err := exec.Command(sysbin.Ifconfig, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("configure %s (%s -> %s): %w (%s)", iface, local, peer, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ApplyFullTunnel routes all IPv4 traffic through the tunnel interface using
// the standard 0.0.0.0/1 + 128.0.0.0/1 split-default trick: two more-specific
// routes outrank the existing default without deleting it, so Restore can
// cleanly remove just the two overrides. It also rejects global IPv6 the
// same way (see ipv6RejectNets), since the tunnel carries IPv4 only.
// Idempotent (delete-then-add, like ProtectServer), so it replaces stale
// routes left by a crashed run. macOS's own network reconciliation
// (IPMonitor) can silently reap these routes even with -static, so Watch
// keeps them in place for the life of the connection — via reassert, which
// only re-adds, never deletes.
func (s *Snapshot) ApplyFullTunnel(tunIface string) error {
	s.tunIface = tunIface
	// Set before the first add, so a partial failure still lets Restore
	// remove whatever did get installed (every removal tolerates "not in
	// table").
	s.overrideAdded = true
	for _, net := range ipv4OverrideNets {
		_ = exec.Command(sysbin.Route, "-n", "delete", "-net", net).Run()
		cmd := exec.Command(sysbin.Route, ipv4OverrideAddArgs(net, tunIface)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("add override route %s via %s: %w (%s)", net, tunIface, err, strings.TrimSpace(string(out)))
		}
	}
	for _, net := range ipv6RejectNets {
		_ = exec.Command(sysbin.Route, ipv6RouteArgs("delete", net)...).Run()
		cmd := exec.Command(sysbin.Route, ipv6RejectAddArgs(net)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("add IPv6 reject route %s/1: %w (%s)", net, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// ipv6RouteArgs is the `route` argv prefix addressing one IPv6 /1 half.
func ipv6RouteArgs(verb, net string) []string {
	return []string{"-n", verb, "-inet6", "-net", net, "-prefixlen", "1"}
}

func ipv6RejectAddArgs(net string) []string {
	return append(ipv6RouteArgs("add", net), "::1", "-reject", "-static")
}

func ipv4OverrideAddArgs(net, tunIface string) []string {
	return []string{"-n", "add", "-static", "-net", net, "-interface", tunIface}
}

func (s *Snapshot) serverRouteAddArgs() []string {
	return []string{"-n", "add", "-static", "-host", s.VPNServerIP, s.DefaultGateway}
}

// reassert re-adds whichever of this snapshot's routes macOS has reaped and
// leaves the ones still present untouched. Unlike ProtectServer and
// ApplyFullTunnel — delete-then-add, which setup needs to replace stale
// routes left by a crashed run — it never opens a window in which a route
// is absent: for the overrides that window would send traffic, IPv6
// included, straight out the physical interface every Watch tick.
func (s *Snapshot) reassert() {
	for _, args := range s.reassertCommands() {
		// "File exists" just means the route is still in place; any
		// other failure is retried on the next tick.
		_ = exec.Command(sysbin.Route, args...).Run()
	}
}

// reassertCommands lists the `route add` argv reassert issues — add-only by
// construction.
func (s *Snapshot) reassertCommands() [][]string {
	var adds [][]string
	if s.hostRouteAdded && s.VPNServerIP != "" {
		adds = append(adds, s.serverRouteAddArgs())
	}
	if s.overrideAdded && s.tunIface != "" {
		for _, net := range ipv4OverrideNets {
			adds = append(adds, ipv4OverrideAddArgs(net, s.tunIface))
		}
		for _, net := range ipv6RejectNets {
			adds = append(adds, ipv6RejectAddArgs(net))
		}
	}
	return adds
}

// Watch periodically re-asserts ProtectServer (and ApplyFullTunnel, if it
// was ever applied) for as long as ctx is alive — a single route add at
// connect time isn't enough for a long session: macOS can reap these
// routes out from under a live connection (see ApplyFullTunnel's doc
// comment), and when the host route to the VPN server specifically goes
// missing, the IKE/ESP socket's next send fails immediately and fatally
// (`sendto: can't assign requested address`) since it falls through to
// whatever less-specific route is left — which, under full-tunnel, points
// right back into the tunnel interface that route was supposed to bypass.
// elevate is called around each reassertion since these need root.
func (s *Snapshot) Watch(ctx context.Context, elevate func(func() error) error, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = elevate(func() error {
				s.reassert()
				return nil
			})
		}
	}
}

// Restore undoes everything Apply*/Protect* did, in reverse order. It is
// safe to call multiple times and safe to call with a zero-value Snapshot
// reconstructed from disk (see Repair), because every removal tolerates
// "route not found" instead of failing.
func (s *Snapshot) Restore() error {
	var errs []string
	if s.overrideAdded {
		for _, net := range ipv4OverrideNets {
			if out, err := exec.Command(sysbin.Route, "-n", "delete", "-net", net).CombinedOutput(); err != nil && !strings.Contains(string(out), "not in table") {
				errs = append(errs, fmt.Sprintf("remove override route %s: %v (%s)", net, err, strings.TrimSpace(string(out))))
			}
		}
		for _, net := range ipv6RejectNets {
			if out, err := exec.Command(sysbin.Route, ipv6RouteArgs("delete", net)...).CombinedOutput(); err != nil && !strings.Contains(string(out), "not in table") {
				errs = append(errs, fmt.Sprintf("remove IPv6 reject route %s/1: %v (%s)", net, err, strings.TrimSpace(string(out))))
			}
		}
	}
	if s.hostRouteAdded && s.VPNServerIP != "" {
		if out, err := exec.Command(sysbin.Route, "-n", "delete", "-host", s.VPNServerIP).CombinedOutput(); err != nil && !strings.Contains(string(out), "not in table") {
			errs = append(errs, fmt.Sprintf("remove VPN server host route: %v (%s)", err, strings.TrimSpace(string(out))))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("route restore had %d issue(s): %s", len(errs), strings.Join(errs, "; "))
	}
	return nil
}

// RestoreByServerIP is used by `repair`, which has no live Snapshot (it may
// run after a crash) — it blindly attempts to remove the well-known override
// routes and the given server's host route, tolerating "already gone".
func RestoreByServerIP(serverIP string) error {
	s := &Snapshot{VPNServerIP: serverIP, overrideAdded: true, hostRouteAdded: serverIP != ""}
	return s.Restore()
}
