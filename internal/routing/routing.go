// Package routing captures the pre-VPN routing state and installs/restores
// the routes needed for a tunnel, on macOS via the `route` and `ifconfig`
// system tools (no netlink equivalent exists on macOS; there is no
// dependency-free way to do this except shelling out to the system route
// command, which every macOS network tool — including Apple's own — does).
package routing

import (
	"fmt"
	"os/exec"
	"strings"
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

// Capture reads the current default route. It must be called before any
// other function in this package changes anything.
func Capture() (*Snapshot, error) {
	out, err := exec.Command("route", "-n", "get", "default").Output()
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
// original gateway, so the tunnel's own encrypted traffic keeps leaving via
// the real interface instead of recursing into the tunnel once the default
// route is overridden.
func (s *Snapshot) ProtectServer(serverIP string) error {
	s.VPNServerIP = serverIP
	// Idempotent: delete-then-add so re-running connect after a crash
	// doesn't fail on "route already exists".
	_ = exec.Command("route", "-n", "delete", "-host", serverIP).Run()
	cmd := exec.Command("route", "-n", "add", "-host", serverIP, s.DefaultGateway)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("add host route to VPN server via %s: %w (%s)", s.DefaultGateway, err, strings.TrimSpace(string(out)))
	}
	s.hostRouteAdded = true
	return nil
}

// ApplyFullTunnel routes all IPv4 traffic through the tunnel interface using
// the standard 0.0.0.0/1 + 128.0.0.0/1 split-default trick: two more-specific
// routes outrank the existing default without deleting it, so Restore can
// cleanly remove just the two overrides.
func (s *Snapshot) ApplyFullTunnel(tunIface string) error {
	s.tunIface = tunIface
	for _, net := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		cmd := exec.Command("route", "-n", "add", "-net", net, "-interface", tunIface)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("add override route %s via %s: %w (%s)", net, tunIface, err, strings.TrimSpace(string(out)))
		}
	}
	s.overrideAdded = true
	return nil
}

// Restore undoes everything Apply*/Protect* did, in reverse order. It is
// safe to call multiple times and safe to call with a zero-value Snapshot
// reconstructed from disk (see Repair), because every removal tolerates
// "route not found" instead of failing.
func (s *Snapshot) Restore() error {
	var errs []string
	if s.overrideAdded {
		for _, net := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
			if out, err := exec.Command("route", "-n", "delete", "-net", net).CombinedOutput(); err != nil && !strings.Contains(string(out), "not in table") {
				errs = append(errs, fmt.Sprintf("remove override route %s: %v (%s)", net, err, strings.TrimSpace(string(out))))
			}
		}
	}
	if s.hostRouteAdded && s.VPNServerIP != "" {
		if out, err := exec.Command("route", "-n", "delete", "-host", s.VPNServerIP).CombinedOutput(); err != nil && !strings.Contains(string(out), "not in table") {
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
