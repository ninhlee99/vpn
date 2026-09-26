// Package dnsmgr configures and restores DNS servers for the VPN session dynamically
// using macOS scutil DynamicStore (in-memory only).
//
// By using in-memory DynamicStore keys (State:/Network/Service/.../DNS), macOS routes
// DNS queries to the VPN DNS servers while the tunnel is active, without writing to
// /Library/Preferences/SystemConfiguration/preferences.plist.
//
// This guarantees that if the Mac abruptly powers off, reboots, or loses power while
// connected, no stale DNS settings are ever left in macOS Network Preferences upon reboot.
package dnsmgr

import (
	"fmt"
	"os/exec"
	"strings"

	"vpn/internal/sysbin"
)

const (
	vpnServiceID = "com.tms.vpn.dns"
	dnsStateKey  = "State:/Network/Service/" + vpnServiceID + "/DNS"
	ipv4StateKey = "State:/Network/Global/IPv4"
)

// Snapshot is the DNS configuration state for the VPN session.
type Snapshot struct {
	Service  string
	Servers  []string
	applied  bool
	TunIface string
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
	s := &Snapshot{Service: service}
	out, err := exec.Command(sysbin.Networksetup, "-getdnsservers", service).Output()
	if err == nil {
		text := strings.TrimSpace(string(out))
		if text != "" && !strings.Contains(text, "aren't any DNS Servers") {
			s.Servers = strings.Fields(text)
		}
	}
	return s, nil
}

// FromRecorded rebuilds a Snapshot from state previously persisted to disk.
func FromRecorded(service string, servers []string, applied bool) *Snapshot {
	return &Snapshot{Service: service, Servers: servers, applied: applied}
}

// Apply sets DNS servers dynamically via scutil DynamicStore (in-memory only).
func (s *Snapshot) Apply(servers []string) error {
	if len(servers) == 0 {
		return nil
	}
	var script strings.Builder
	script.WriteString("d.init\n")
	script.WriteString(fmt.Sprintf("d.add ServerAddresses * %s\n", strings.Join(servers, " ")))
	script.WriteString("d.add SupplementalMatchDomains * \"\"\n")
	if s.TunIface != "" {
		script.WriteString(fmt.Sprintf("d.add InterfaceName %s\n", s.TunIface))
	}
	script.WriteString(fmt.Sprintf("set %s\n", dnsStateKey))
	script.WriteString("d.init\n")
	script.WriteString(fmt.Sprintf("d.add PrimaryService %s\n", vpnServiceID))
	script.WriteString(fmt.Sprintf("d.add Services * %s\n", vpnServiceID))
	script.WriteString(fmt.Sprintf("set %s\n", ipv4StateKey))

	cmd := exec.Command(sysbin.Scutil)
	cmd.Stdin = strings.NewReader(script.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("apply dynamic DNS via scutil: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	s.applied = true
	return nil
}

// Restore removes the dynamic scutil DNS keys and cleans any legacy networksetup DNS.
func (s *Snapshot) Restore() error {
	var script strings.Builder
	script.WriteString(fmt.Sprintf("remove %s\n", dnsStateKey))
	script.WriteString(fmt.Sprintf("remove %s\n", ipv4StateKey))

	cmd := exec.Command(sysbin.Scutil)
	cmd.Stdin = strings.NewReader(script.String())
	_ = cmd.Run()

	// Clean up any legacy persistent DNS left on the Wi-Fi/Ethernet service by previous versions
	if s.Service != "" {
		out, err := exec.Command(sysbin.Networksetup, "-getdnsservers", s.Service).Output()
		if err == nil {
			text := strings.TrimSpace(string(out))
			if text != "" && !strings.Contains(text, "aren't any DNS Servers") {
				_ = exec.Command(sysbin.Networksetup, "-setdnsservers", s.Service, "Empty").Run()
			}
		}
	}
	return nil
}

// CleanPersistentSettings removes any leftover persistent DNS settings from all network services.
func CleanPersistentSettings() {
	out, err := exec.Command(sysbin.Networksetup, "-listallnetworkservices").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		svc := strings.TrimSpace(line)
		if svc == "" || strings.Contains(svc, "*") {
			continue
		}
		cur, err := exec.Command(sysbin.Networksetup, "-getdnsservers", svc).Output()
		if err == nil {
			text := strings.TrimSpace(string(cur))
			if text != "" && !strings.Contains(text, "aren't any DNS Servers") {
				_ = exec.Command(sysbin.Networksetup, "-setdnsservers", svc, "Empty").Run()
			}
		}
	}
}
