package dnsmgr

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Live check against the real SystemConfiguration store. Needs root and
// changes (then restores) system state, so it only runs when asked:
//
//	sudo VPN_LIVE_SCUTIL=1 go test ./internal/dnsmgr -run TestLiveApplyRestore -v
func TestLiveApplyRestore(t *testing.T) {
	if os.Getenv("VPN_LIVE_SCUTIL") != "1" || os.Geteuid() != 0 {
		t.Skip("set VPN_LIVE_SCUTIL=1 and run as root")
	}
	show := func(key string) string {
		c := exec.Command("/usr/sbin/scutil")
		c.Stdin = strings.NewReader("show " + key + "\n")
		out, _ := c.CombinedOutput()
		return string(out)
	}
	defaultRoute := func() string {
		out, _ := exec.Command("/usr/sbin/netstat", "-rn", "-f", "inet").Output()
		for _, l := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(l, "default") {
				return strings.Join(strings.Fields(l), " ")
			}
		}
		return ""
	}
	beforeRoute, beforeGlobalDNS := defaultRoute(), show("State:/Network/Global/DNS")

	s := &Snapshot{TunIface: "lo0", LocalIP: "10.99.99.1", PeerIP: "10.99.99.2"}
	t.Cleanup(func() { _ = s.Restore() })
	if err := s.Apply([]string{"8.8.8.8", "1.1.1.1"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	if d := show(dnsStateKey); !strings.Contains(d, "8.8.8.8") {
		t.Errorf("DNS service not published:\n%s", d)
	}
	if v := show(ipv4StateKey); !strings.Contains(v, "10.99.99.1") || !strings.Contains(v, "lo0") {
		t.Errorf("IPv4 service not published:\n%s", v)
	}
	if r := defaultRoute(); r != beforeRoute {
		t.Errorf("default route changed while registered: %q -> %q", beforeRoute, r)
	}
	if err := s.Restore(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	for _, k := range []string{dnsStateKey, ipv4StateKey} {
		if out := show(k); !strings.Contains(out, "No such key") {
			t.Errorf("%s not removed:\n%s", k, out)
		}
	}
	if r := defaultRoute(); r != beforeRoute {
		t.Errorf("default route changed after restore: %q -> %q", beforeRoute, r)
	}
	if g := show("State:/Network/Global/DNS"); g != beforeGlobalDNS {
		t.Errorf("configd's Global/DNS changed:\nbefore %s\nafter %s", beforeGlobalDNS, g)
	}
}
