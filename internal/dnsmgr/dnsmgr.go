// Package dnsmgr configures and restores DNS servers for the VPN session dynamically
// using macOS scutil DynamicStore (in-memory only).
//
// By using in-memory DynamicStore keys (State:/Network/Service/.../DNS and State:/Network/Global/DNS),
// macOS routes DNS queries to the VPN DNS servers while the tunnel is active, without writing to
// /Library/Preferences/SystemConfiguration/preferences.plist.
//
// This guarantees that if the Mac abruptly powers off, reboots, or loses power while
// connected, no stale DNS settings are ever left in macOS Network Preferences upon reboot.
package dnsmgr

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	"vpn/internal/sysbin"
)

const (
	vpnServiceID = "com.tms.vpn.dns"
	dnsStateKey  = "State:/Network/Service/" + vpnServiceID + "/DNS"
	ipv4StateKey = "State:/Network/Service/" + vpnServiceID + "/IPv4"
)

// Snapshot is the DNS configuration state for the VPN session.
type Snapshot struct {
	Service  string
	Servers  []string
	applied  bool
	TunIface string
	// LocalIP/PeerIP, when set, are published as the service's IPv4 state so
	// configd knows the tunnel interface belongs to a service — as it does
	// for the built-in VPN clients — instead of an unregistered utun.
	LocalIP string
	PeerIP  string
}

// isPrivateIPv4 returns whether the given IP address is an RFC 1918 or CGNAT private IPv4 address.
func isPrivateIPv4(ipStr string) bool {
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return false
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	// 10.0.0.0/8
	if ip4[0] == 10 {
		return true
	}
	// 172.16.0.0/12 (172.16.0.0 - 172.31.255.255)
	if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
		return true
	}
	// 192.168.0.0/16
	if ip4[0] == 192 && ip4[1] == 168 {
		return true
	}
	// 100.64.0.0/10 (CGNAT)
	if ip4[0] == 100 && (ip4[1]&0xc0) == 64 {
		return true
	}
	return false
}

// PrioritizeDNSServers orders private corporate DNS servers first, followed by public DNS servers,
// and deduplicates the list while preserving order.
func PrioritizeDNSServers(servers []string) []string {
	seen := make(map[string]bool)
	var privateServers, publicServers []string
	for _, s := range servers {
		s = strings.TrimSpace(s)
		if s == "" || s == "0.0.0.0" || seen[s] {
			continue
		}
		seen[s] = true
		if isPrivateIPv4(s) {
			privateServers = append(privateServers, s)
		} else {
			publicServers = append(publicServers, s)
		}
	}
	return append(privateServers, publicServers...)
}

// RankByReachability keeps the given order but moves servers for which alive
// returns false behind those that answer. A pushed server that is
// unreachable from here (typically because it sits in the same subnet as the
// local network, or the LNS does not forward to it) must not be the one every
// lookup waits on first; the second result is how many answered. Nothing is dropped: an unreachable server may just
// be slow, and dropping it could leave no resolver at all.
func RankByReachability(servers []string, alive func(string) bool) (ranked []string, reachable int) {
	ok := make([]bool, len(servers))
	var wg sync.WaitGroup
	for i, srv := range servers {
		wg.Add(1)
		go func(i int, srv string) {
			defer wg.Done()
			ok[i] = alive(srv)
		}(i, srv)
	}
	wg.Wait()
	var up, down []string
	for i, srv := range servers {
		if ok[i] {
			up = append(up, srv)
		} else {
			down = append(down, srv)
		}
	}
	return append(up, down...), len(up)
}

// dnsQuery is a minimal recursive A query for example.com with the given ID.
func dnsQuery(id uint16) []byte {
	q := []byte{byte(id >> 8), byte(id), 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, label := range []string{"example", "com"} {
		q = append(q, byte(len(label)))
		q = append(q, label...)
	}
	return append(q, 0, 0, 1, 0, 1)
}

// ProbeServer reports whether a DNS server at addr ("host:port") answers a
// query within timeout. Any well-formed response counts — even NXDOMAIN or
// REFUSED proves the path to the server works, which is all ranking needs.
func ProbeServer(addr string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("udp", addr, timeout)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	const id = 0x5a17
	if _, err := conn.Write(dnsQuery(id)); err != nil {
		return false
	}
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	// QR bit set and our transaction ID echoed.
	return err == nil && n >= 12 && buf[0] == byte(id>>8) && buf[1] == byte(id&0xff) && buf[2]&0x80 != 0
}

// FlushCache purges macOS DNS cache and notifies mDNSResponder to reload immediately.
func FlushCache() {
	_ = exec.Command(sysbin.Dscacheutil, "-flushcache").Run()
	_ = exec.Command(sysbin.Killall, "-HUP", "mDNSResponder").Run()
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

// cleanServers drops blanks, 0.0.0.0 and duplicates, preserving the caller's
// order (so a reachability ranking survives to Apply).
func cleanServers(servers []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, s := range servers {
		s = strings.TrimSpace(s)
		if s == "" || s == "0.0.0.0" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// applyScript is the scutil script that publishes the VPN's DNS service, in
// the given order, as a match-everything resolver bound to tunIface.
func applyScript(servers []string, tunIface string) string {
	var script strings.Builder
	script.WriteString("d.init\n")
	script.WriteString(fmt.Sprintf("d.add ServerAddresses * %s\n", strings.Join(servers, " ")))
	script.WriteString("d.add SupplementalMatchDomains * \"\"\n")
	script.WriteString("d.add SupplementalMatchOrders * 100000\n")
	if tunIface != "" {
		script.WriteString(fmt.Sprintf("d.add InterfaceName %s\n", tunIface))
	}
	script.WriteString(fmt.Sprintf("set %s\n", dnsStateKey))
	return script.String()
}

// applyScript's IPv4 counterpart. Deliberately no Router key: measured on
// macOS 15, a point-to-point Addresses/DestAddresses entry without Router
// leaves the primary service and default route untouched, so this cannot
// steal the default route from the physical interface (split-tunnel safe).
func ipv4Script(tunIface, local, peer string) string {
	if tunIface == "" || net.ParseIP(local) == nil {
		return ""
	}
	var script strings.Builder
	script.WriteString("d.init\n")
	script.WriteString(fmt.Sprintf("d.add InterfaceName %s\n", tunIface))
	script.WriteString(fmt.Sprintf("d.add Addresses * %s\n", local))
	if net.ParseIP(peer) != nil {
		script.WriteString(fmt.Sprintf("d.add DestAddresses * %s\n", peer))
	}
	script.WriteString(fmt.Sprintf("set %s\n", ipv4StateKey))
	return script.String()
}

// Apply sets DNS servers dynamically via scutil DynamicStore (in-memory only).
// Order is preserved: callers rank with PrioritizeDNSServers and
// RankByReachability first.
func (s *Snapshot) Apply(servers []string) error {
	ordered := cleanServers(servers)
	if len(ordered) == 0 {
		return nil
	}
	script := applyScript(ordered, s.TunIface) + ipv4Script(s.TunIface, s.LocalIP, s.PeerIP)

	cmd := exec.Command(sysbin.Scutil)
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("apply dynamic DNS via scutil: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	s.applied = true

	// Purge stale negative DNS cache immediately so private hosts resolve on first try
	FlushCache()
	return nil
}

// restoreScript removes only our own service keys (DNS and IPv4). State:/Network/Global/DNS
// is owned and regenerated by configd; removing it used to leave the machine
// with no resolvers until the next network change (no internet even after
// the VPN was off).
func restoreScript() string {
	return fmt.Sprintf("remove %s\nremove %s\n", dnsStateKey, ipv4StateKey)
}

// Restore removes the dynamic scutil DNS keys and cleans any legacy networksetup DNS.
func (s *Snapshot) Restore() error {
	script := restoreScript()

	cmd := exec.Command(sysbin.Scutil)
	cmd.Stdin = strings.NewReader(script)
	_ = cmd.Run()

	// Flush cache after removing VPN DNS keys so macOS reverts immediately to physical interface DNS
	FlushCache()

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
	FlushCache()
}
