package engine

import (
	"context"
	"fmt"
	"net"
	"time"

	"vpn/internal/netwatch"
	"vpn/internal/vpnlog"
)

// Network-event handling. A Wi-Fi roam, an unplugged cable or a wake from
// sleep can leave the tunnel unable to work while every timer still looks
// healthy; the kernel says so immediately, and acting on it beats waiting out
// the liveness watchdog. Both delays are vars so tests can shrink them.
var (
	routeSettle = time.Second     // quiet time after a route deletion before re-adding our routes
	linkSettle  = 2 * time.Second // quiet time after a link/address change before judging it
	probeWait   = 5 * time.Second // how long the LNS gets to answer the probe sent after a link change
)

// watchNetwork consumes routing-socket events for as long as ctx lives:
//
//   - a deleted route triggers kickRoutes (route reassertion), debounced;
//   - a link/address change first checks that our local address still exists
//     (if not, the tunnel is certainly dead → drop at once), otherwise sends a
//     probe and drops only if the server does not answer it.
//
// Being event-driven it costs nothing while the network is quiet.
func watchNetwork(ctx context.Context, events <-chan netwatch.Kind, localIP net.IP, live *liveness,
	probe func(), kickRoutes chan<- struct{}, drop func(error), addrPresent func(net.IP) bool) {

	var routeTimer, linkTimer <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case k, ok := <-events:
			if !ok {
				vpnlog.Error("ENGINE", "network event feed ended — relying on the liveness watchdog alone", nil)
				return
			}
			vpnlog.Info("ENGINE", "network event", vpnlog.Fields{"kind": k.String()})
			switch k {
			case netwatch.RouteDeleted:
				if routeTimer == nil {
					routeTimer = time.After(routeSettle)
				}
			case netwatch.LinkChanged:
				if linkTimer == nil {
					linkTimer = time.After(linkSettle)
				}
			}
		case <-routeTimer:
			routeTimer = nil
			select {
			case kickRoutes <- struct{}{}:
			default: // a reassertion is already queued
			}
		case <-linkTimer:
			linkTimer = nil
			if !addrPresent(localIP) {
				drop(fmt.Errorf("network changed: local address %s is no longer assigned to any interface", localIP))
				return
			}
			before := live.rx.Load()
			probe()
			select {
			case <-ctx.Done():
				return
			case <-time.After(probeWait):
			}
			if live.rx.Load() == before {
				drop(fmt.Errorf("network changed and the server did not answer a probe within %s", probeWait))
				return
			}
			vpnlog.Info("ENGINE", "network changed but the tunnel still answers", nil)
		}
	}
}

// localAddrPresent reports whether ip is currently assigned to an interface
// that is up — cheap (two syscalls), and only called on a network event.
func localAddrPresent(ip net.IP) bool {
	ifs, err := net.Interfaces()
	if err != nil {
		return true // cannot tell: let the probe decide
	}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
				return true
			}
		}
	}
	return false
}
