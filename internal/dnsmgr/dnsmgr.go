// Package dnsmgr captures and restores the DNS servers configured on the
// active network service, via the `networksetup` system tool (the only
// documented way to change per-service DNS on macOS without touching
// SystemConfiguration internals directly).
package dnsmgr

import (
	"fmt"
	"os/exec"
	"strings"

	"vpn/internal/sysbin"
)

// Snapshot is the pre-VPN DNS configuration for one network service.
type Snapshot struct {
	Service string
	Servers []string // empty means "Empty" (DHCP-assigned), not "no snapshot"
	applied bool
}

// ServiceForInterface maps a BSD interface name (e.g. "en0") to the
// networksetup service name (e.g. "Wi-Fi") that controls it.
func ServiceForInterface(iface string) (string, error) {
	out, err := exec.Command(sysbin.Networksetup, "-listallhardwareports").Output()
	if err != nil {
		return "", fmt.Errorf("list hardware ports: %w", err)
	}
	var service, device string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Hardware Port:"):
			service = strings.TrimSpace(strings.TrimPrefix(line, "Hardware Port:"))
		case strings.HasPrefix(line, "Device:"):
			device = strings.TrimSpace(strings.TrimPrefix(line, "Device:"))
			if device == iface {
				return service, nil
			}
		}
	}
	return "", fmt.Errorf("no network service found for interface %s", iface)
}

// Capture reads the current DNS servers for service.
func Capture(service string) (*Snapshot, error) {
	out, err := exec.Command(sysbin.Networksetup, "-getdnsservers", service).Output()
	if err != nil {
		return nil, fmt.Errorf("read DNS servers for %s: %w", service, err)
	}
	text := strings.TrimSpace(string(out))
	s := &Snapshot{Service: service}
	if text != "" && !strings.Contains(text, "aren't any DNS Servers") {
		s.Servers = strings.Fields(text)
	}
	return s, nil
}

// FromRecorded rebuilds a Snapshot from state previously persisted to disk
// (see internal/state.State's DNS* fields) — used by disconnect/repair when
// they're a separate process invocation from the one that called Apply, so
// they have no live *Snapshot to call Restore on.
func FromRecorded(service string, servers []string, applied bool) *Snapshot {
	return &Snapshot{Service: service, Servers: servers, applied: applied}
}

// Apply sets the service's DNS servers to those pushed by the VPN server. An
// empty list is a no-op (the goal is never to silently hand out public DNS
// the server didn't provide).
func (s *Snapshot) Apply(servers []string) error {
	if len(servers) == 0 {
		return nil
	}
	args := append([]string{"-setdnsservers", s.Service}, servers...)
	if out, err := exec.Command(sysbin.Networksetup, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("set DNS servers on %s: %w (%s)", s.Service, err, strings.TrimSpace(string(out)))
	}
	s.applied = true
	return nil
}

// Restore puts the service's DNS back exactly as captured — "Empty" restores
// DHCP-assigned DNS rather than hard-coding anything.
func (s *Snapshot) Restore() error {
	if !s.applied {
		return nil
	}
	args := []string{"-setdnsservers", s.Service, "Empty"}
	if len(s.Servers) > 0 {
		args = append([]string{"-setdnsservers", s.Service}, s.Servers...)
	}
	if out, err := exec.Command(sysbin.Networksetup, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("restore DNS servers on %s: %w (%s)", s.Service, err, strings.TrimSpace(string(out)))
	}
	return nil
}
