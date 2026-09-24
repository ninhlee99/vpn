package engine

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"vpn/internal/netwatch"
)

func fastNet(t *testing.T) {
	r, l, p := routeSettle, linkSettle, probeWait
	routeSettle, linkSettle, probeWait = 10*time.Millisecond, 10*time.Millisecond, 60*time.Millisecond
	t.Cleanup(func() { routeSettle, linkSettle, probeWait = r, l, p })
}

type netHarness struct {
	events chan netwatch.Kind
	kick   chan struct{}
	drops  chan error
	probes int
	mu     sync.Mutex
}

func runNet(t *testing.T, present bool, live *liveness, onProbe func()) (*netHarness, context.CancelFunc) {
	fastNet(t)
	h := &netHarness{events: make(chan netwatch.Kind, 4), kick: make(chan struct{}, 1), drops: make(chan error, 1)}
	ctx, cancelCtx := context.WithCancel(context.Background())
	done := make(chan struct{})
	cancel := func() { cancelCtx(); <-done } // the watcher reads the package vars: it must be gone before they are restored
	go func() {
		defer close(done)
		watchNetwork(ctx, h.events, net.IPv4(10, 0, 0, 5), live,
			func() {
				h.mu.Lock()
				h.probes++
				h.mu.Unlock()
				if onProbe != nil {
					onProbe()
				}
			},
			h.kick, func(err error) { h.drops <- err }, func(net.IP) bool { return present })
	}()
	return h, cancel
}

func TestNetworkRouteDeletionKicksReassertion(t *testing.T) {
	h, cancel := runNet(t, true, newLiveness(), nil)
	defer cancel()
	h.events <- netwatch.RouteDeleted
	h.events <- netwatch.RouteDeleted // a burst collapses into one kick
	select {
	case <-h.kick:
	case <-time.After(time.Second):
		t.Fatal("route deletion did not trigger a reassertion")
	}
}

func TestNetworkLostAddressDropsImmediatelyWithoutProbe(t *testing.T) {
	h, cancel := runNet(t, false, newLiveness(), nil)
	defer cancel()
	h.events <- netwatch.LinkChanged
	select {
	case <-h.drops:
	case <-time.After(time.Second):
		t.Fatal("a vanished local address must end the tunnel")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.probes != 0 {
		t.Fatal("no point probing a tunnel whose local address is gone")
	}
}

func TestNetworkChangeKeepsTunnelThatAnswersProbe(t *testing.T) {
	live := newLiveness()
	h, cancel := runNet(t, true, live, func() { live.touch() }) // the LNS answers
	defer cancel()
	h.events <- netwatch.LinkChanged
	select {
	case err := <-h.drops:
		t.Fatalf("a tunnel that still answers was dropped: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestNetworkChangeDropsSilentTunnel(t *testing.T) {
	h, cancel := runNet(t, true, newLiveness(), nil) // nothing ever answers
	defer cancel()
	h.events <- netwatch.LinkChanged
	select {
	case <-h.drops:
	case <-time.After(time.Second):
		t.Fatal("a tunnel that ignores the probe after a network change must be dropped")
	}
}
