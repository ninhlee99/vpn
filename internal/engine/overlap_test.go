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

func TestRerankDNS(t *testing.T) {
	alive := func(a map[string]bool) func(string) bool { return func(s string) bool { return a[s] } }
	var applied []string
	apply := func(r []string) error { applied = r; return nil }

	// A dead first server: the answering one moves up and is applied.
	changed, n, err := rerankDNS([]string{"10.0.0.1", "8.8.8.8"}, alive(map[string]bool{"8.8.8.8": true}), apply)
	if err != nil || !changed || n != 1 || len(applied) != 2 || applied[0] != "8.8.8.8" {
		t.Fatalf("changed=%v n=%d err=%v applied=%v", changed, n, err, applied)
	}
	// Already in a good order: nothing applied.
	applied = nil
	if changed, _, _ := rerankDNS([]string{"8.8.8.8", "10.0.0.1"}, alive(map[string]bool{"8.8.8.8": true}), apply); changed || applied != nil {
		t.Fatalf("re-applied an unchanged order: %v", applied)
	}
	// Nothing answers: keep the static order, apply nothing.
	if changed, n, _ := rerankDNS([]string{"10.0.0.1"}, alive(nil), apply); changed || n != 0 || applied != nil {
		t.Fatalf("applied with no reachable server: %v", applied)
	}
}
