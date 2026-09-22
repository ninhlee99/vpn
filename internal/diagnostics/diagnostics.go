// Package diagnostics inspects the local network and a VPN server's
// reachability before a real connection attempt, so failures can be
// attributed to a specific stage instead of a generic "VPN failed".
package diagnostics

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Absolute paths, not bare command names — same PATH-hijack concern as
// routing.go/dnsmgr.go/keychain.go, and consistent hardening even though
// diagnose itself never runs privileged.
const (
	routeBin    = "/sbin/route"
	ifconfigBin = "/sbin/ifconfig"
	scutilBin   = "/usr/sbin/scutil"
	pingBin     = "/sbin/ping"
)

// Stage names used to report where a probe stopped succeeding — kept as
// constants so callers (CLI, engine) can match on them instead of parsing
// free text.
const (
	StageDNS     = "DNS_FAILURE"
	StageUDP500  = "IKE_TIMEOUT"
	StageUDP4500 = "NAT_T_FAILURE"
	StageMTU     = "MTU_FAILURE"
	StageNone    = "" // no failure observed
)

// Network describes the active outbound network path.
type Network struct {
	Interface  string   `json:"interface"`
	IPv4       string   `json:"ipv4"`
	Gateway    string   `json:"gateway"`
	MTU        int      `json:"mtu"`
	DNSServers []string `json:"dns_servers"`
}

// Connectivity describes reachability of the VPN server.
type Connectivity struct {
	ServerHost     string   `json:"server_host"`
	ResolvedIPs    []string `json:"resolved_ips"`
	DNSOK          bool     `json:"dns_ok"`
	UDP500Reached  bool     `json:"udp_500_reached"`
	UDP4500Reached bool     `json:"udp_4500_reached"`
	PingRTTMillis  float64  `json:"ping_rtt_ms,omitempty"`
}

// MTUProbe is the result of testing one packet size for fragmentation.
type MTUProbe struct {
	Size int  `json:"size"`
	OK   bool `json:"ok"`
}

// Report is the full diagnostics result, safe to marshal as --json: it never
// contains credentials.
type Report struct {
	Network      Network      `json:"network"`
	Connectivity Connectivity `json:"connectivity"`
	MTUProbes    []MTUProbe   `json:"mtu_probes"`
	FailureStage string       `json:"failure_stage,omitempty"`
	Errors       []string     `json:"errors,omitempty"`
}

// probeSizes mirrors the goal's requested MTU sweep.
var probeSizes = []int{1500, 1492, 1400, 1380, 1360, 1280}

// Run performs every diagnostic against server (host or IP, no port).
func Run(ctx context.Context, server string) *Report {
	r := &Report{}

	r.Network = probeNetwork()

	r.Connectivity = Connectivity{ServerHost: server}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, server)
	if err != nil {
		r.Errors = append(r.Errors, fmt.Sprintf("DNS resolution of %s failed: %v", server, err))
		r.FailureStage = StageDNS
		return r
	}
	r.Connectivity.DNSOK = true
	for _, ip := range ips {
		r.Connectivity.ResolvedIPs = append(r.Connectivity.ResolvedIPs, ip.String())
	}
	target := r.Connectivity.ResolvedIPs[0]

	if rtt, err := pingRTT(target); err == nil {
		r.Connectivity.PingRTTMillis = rtt
	}

	r.Connectivity.UDP500Reached = udpProbe(ctx, target, 500)
	r.Connectivity.UDP4500Reached = udpProbe(ctx, target, 4500)
	if !r.Connectivity.UDP500Reached && !r.Connectivity.UDP4500Reached {
		r.FailureStage = StageUDP500
	}

	r.MTUProbes = probeMTU(target)
	for _, p := range r.MTUProbes {
		if !p.OK && p.Size >= 1400 && r.FailureStage == StageNone {
			// A black hole above 1400 (the common PPPoE/VPN-in-VPN ceiling)
			// is worth flagging even when connectivity otherwise looks fine.
			r.FailureStage = StageMTU
		}
	}

	return r
}

func probeNetwork() Network {
	n := Network{}

	if out, err := exec.Command(routeBin, "-n", "get", "default").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "interface:"):
				n.Interface = strings.TrimSpace(strings.TrimPrefix(line, "interface:"))
			case strings.HasPrefix(line, "gateway:"):
				n.Gateway = strings.TrimSpace(strings.TrimPrefix(line, "gateway:"))
			}
		}
	}

	if n.Interface != "" {
		if out, err := exec.Command(ifconfigBin, n.Interface).Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "inet ") {
					fields := strings.Fields(line)
					if len(fields) >= 2 {
						n.IPv4 = fields[1]
					}
				}
				// The mtu field trails the flags line ("en0: flags=... mtu 1500"),
				// it doesn't start its own line.
				if idx := strings.Index(line, "mtu "); idx >= 0 {
					fields := strings.Fields(line[idx:])
					if len(fields) >= 2 {
						if v, err := strconv.Atoi(fields[1]); err == nil {
							n.MTU = v
						}
					}
				}
			}
		}
	}

	if out, err := exec.Command(scutilBin, "--dns").Output(); err == nil {
		seen := map[string]bool{}
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "nameserver[") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					ip := strings.TrimSpace(parts[1])
					if ip != "" && !seen[ip] {
						seen[ip] = true
						n.DNSServers = append(n.DNSServers, ip)
					}
				}
			}
		}
	}

	return n
}

func pingRTT(ip string) (float64, error) {
	out, err := exec.Command(pingBin, "-c", "2", "-t", "3", ip).Output()
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "min/avg/max") {
			// round-trip min/avg/max/stddev = 82.916/83.100/83.284/0.184 ms
			parts := strings.Split(line, "=")
			if len(parts) != 2 {
				continue
			}
			nums := strings.Split(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(parts[1]), "ms")), "/")
			if len(nums) >= 2 {
				if v, err := strconv.ParseFloat(strings.TrimSpace(nums[1]), 64); err == nil {
					return v, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("no RTT in ping output")
}

// udpProbe reports whether a UDP send to host:port completes without an
// immediate ICMP port-unreachable — this cannot prove a listener answered
// (UDP has no handshake), only that nothing actively refused it, which is
// exactly the signal diagnose needs before a real IKE attempt.
func udpProbe(ctx context.Context, host string, port int) bool {
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "udp4", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	defer conn.Close()
	// One-byte probe. A real ISAKMP/ESP listener stays silent for a bare
	// probe; the goal here is only to catch a hard ICMP refusal, which
	// surfaces as a write or immediate read error, not a timeout.
	if _, err := conn.Write([]byte{0}); err != nil {
		return false
	}
	_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buf := make([]byte, 1)
	_, err = conn.Read(buf)
	if err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return true // silence, not refusal — treated as reachable
		}
		return false // ICMP unreachable surfaces as a read error here
	}
	return true
}

func probeMTU(ip string) []MTUProbe {
	results := make([]MTUProbe, 0, len(probeSizes))
	for _, size := range probeSizes {
		// icmp payload = size - 28 (20 IP + 8 ICMP header)
		payload := size - 28
		if payload < 0 {
			payload = 0
		}
		cmd := exec.Command(pingBin, "-c", "1", "-t", "2", "-D", "-s", strconv.Itoa(payload), ip)
		err := cmd.Run()
		results = append(results, MTUProbe{Size: size, OK: err == nil})
	}
	return results
}
