package dnsmgr

import (
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIsPrivateIPv4(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.1", true},
		{"10.255.255.254", true},
		{"172.16.0.1", true},
		{"172.31.255.254", true},
		{"172.32.0.1", false},
		{"192.168.1.1", true},
		{"192.168.100.1", true},
		{"100.64.0.1", true},
		{"100.127.255.254", true},
		{"100.128.0.1", false},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"118.238.201.33", false},
		{"invalid-ip", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			if got := isPrivateIPv4(tt.ip); got != tt.want {
				t.Errorf("isPrivateIPv4(%q) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestPrioritizeDNSServers(t *testing.T) {
	tests := []struct {
		name    string
		servers []string
		want    []string
	}{
		{
			name:    "public before private",
			servers: []string{"118.238.201.33", "192.168.100.1"},
			want:    []string{"192.168.100.1", "118.238.201.33"},
		},
		{
			name:    "already prioritized with duplicates and invalid",
			servers: []string{"192.168.100.1", "10.0.0.1", "118.238.201.33", "192.168.100.1", "", "0.0.0.0", "8.8.8.8"},
			want:    []string{"192.168.100.1", "10.0.0.1", "118.238.201.33", "8.8.8.8"},
		},
		{
			name:    "all private",
			servers: []string{"10.1.2.3", "192.168.1.1"},
			want:    []string{"10.1.2.3", "192.168.1.1"},
		},
		{
			name:    "all public",
			servers: []string{"8.8.8.8", "1.1.1.1"},
			want:    []string{"8.8.8.8", "1.1.1.1"},
		},
		{
			name:    "empty",
			servers: []string{},
			want:    []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PrioritizeDNSServers(tt.servers)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("PrioritizeDNSServers(%v) = %v, want %v", tt.servers, got, tt.want)
			}
		})
	}
}

func TestCleanServersKeepsCallerOrder(t *testing.T) {
	got := cleanServers([]string{"118.238.201.33", " 192.168.100.1 ", "", "0.0.0.0", "118.238.201.33"})
	want := []string{"118.238.201.33", "192.168.100.1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanServers = %v, want %v", got, want)
	}
}

func TestApplyScript(t *testing.T) {
	got := applyScript([]string{"118.238.201.33", "192.168.100.1"}, "utun5")
	for _, want := range []string{
		"d.add ServerAddresses * 118.238.201.33 192.168.100.1\n",
		"d.add InterfaceName utun5\n",
		"set " + dnsStateKey + "\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("script missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(applyScript([]string{"1.1.1.1"}, ""), "InterfaceName") {
		t.Error("InterfaceName written without a tunnel interface")
	}
}

// Restore must only ever touch our own key: State:/Network/Global/DNS belongs
// to configd, and deleting it left machines with no DNS after disconnect.
func TestRestoreScriptTouchesOnlyOwnKey(t *testing.T) {
	got := restoreScript()
	if strings.Contains(got, "Global") {
		t.Fatalf("restore touches a configd-owned key:\n%s", got)
	}
	if got != "remove "+dnsStateKey+"\n" {
		t.Fatalf("unexpected restore script %q", got)
	}
}

func TestRankByReachability(t *testing.T) {
	alive := map[string]bool{"118.238.201.33": true, "8.8.8.8": true}
	got, n := RankByReachability([]string{"192.168.100.1", "118.238.201.33", "8.8.8.8"}, func(s string) bool { return alive[s] })
	want := []string{"118.238.201.33", "8.8.8.8", "192.168.100.1"}
	if !reflect.DeepEqual(got, want) || n != 2 {
		t.Fatalf("got %v (%d reachable), want %v (2)", got, n, want)
	}
	// Nothing answers: order is kept, nothing is dropped.
	got, n = RankByReachability([]string{"10.0.0.1", "10.0.0.2"}, func(string) bool { return false })
	if !reflect.DeepEqual(got, []string{"10.0.0.1", "10.0.0.2"}) || n != 0 {
		t.Fatalf("all-dead case: got %v (%d)", got, n)
	}
}

// fakeDNS answers one UDP query by echoing it with QR set, or stays silent.
func fakeDNS(t *testing.T, reply bool) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 512)
		n, addr, err := pc.ReadFrom(buf)
		if err != nil || !reply {
			return
		}
		buf[2] |= 0x80
		_, _ = pc.WriteTo(buf[:n], addr)
	}()
	return pc.LocalAddr().String()
}

func TestProbeServer(t *testing.T) {
	if !ProbeServer(fakeDNS(t, true), time.Second) {
		t.Error("responding server reported dead")
	}
	if ProbeServer(fakeDNS(t, false), 200*time.Millisecond) {
		t.Error("silent server reported alive")
	}
}
