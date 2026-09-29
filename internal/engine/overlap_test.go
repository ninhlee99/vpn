package engine

import (
	"net"
	"testing"
)

// lo0 always carries 127.0.0.1/8, standing in for a physical LAN interface.
func TestNetworkOverlapWarnings(t *testing.T) {
	w := networkOverlapWarnings("lo0", net.ParseIP("127.0.0.5"), []string{"127.0.0.1", "8.8.8.8"})
	if len(w) != 2 {
		t.Fatalf("want a warning for the VPN address and one DNS server, got %q", w)
	}
	if w := networkOverlapWarnings("lo0", net.ParseIP("10.64.64.64"), []string{"8.8.8.8"}); len(w) != 0 {
		t.Fatalf("no overlap expected, got %q", w)
	}
	if w := networkOverlapWarnings("nonexistent0", net.ParseIP("127.0.0.1"), nil); len(w) != 0 {
		t.Fatalf("unknown interface must not warn, got %q", w)
	}
}
