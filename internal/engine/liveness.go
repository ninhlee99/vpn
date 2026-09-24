package engine

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

// Liveness timing. The client probes the peer itself instead of waiting for
// the server to: an idle tunnel may legitimately hear nothing for a long
// while, and a dead one looks exactly the same from a blocked read.
const (
	// 20s sits under the ~30s minimum UDP mapping timeout of common NATs while
	// costing a laptop only three tiny packets a minute; deadAfter is three
	// missed probes. Faster detection of a real change comes from the
	// kernel's network events (netevents.go), not from a shorter timer.
	keepaliveEvery = 20 * time.Second // LCP echo + NAT keepalive: keeps NAT mappings alive and provokes a reply
	deadAfter      = 60 * time.Second // this long without one valid packet from the server = peer gone
	statsEvery     = 3                // log a "tunnel alive" line every statsEvery keepalive ticks
)

// liveness records what the data plane last heard from the server, so the
// watchdog can tell a quiet tunnel from a dead one.
type liveness struct {
	lastRx atomic.Int64 // unix nanos of the last ESP packet that decrypted successfully
	rx, tx atomic.Uint64
}

func newLiveness() *liveness {
	l := &liveness{}
	l.touch()
	return l
}

func (l *liveness) touch() {
	l.lastRx.Store(time.Now().UnixNano())
	l.rx.Add(1)
}

func (l *liveness) idle() time.Duration {
	return time.Since(time.Unix(0, l.lastRx.Load()))
}

// watchdog runs until ctx ends or the peer is judged dead, calling probe
// every `every` and report every statsEvery ticks. It returns a non-nil
// error only for a dead peer.
func watchdog(ctx context.Context, every, dead time.Duration, live *liveness, probe func(), report func(idle time.Duration)) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for tick := 1; ; tick++ {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		if idle := live.idle(); idle > dead {
			return fmt.Errorf("peer unresponsive: no valid packet from the server for %s (probing every %s)", idle.Round(time.Second), every)
		}
		probe()
		if tick%statsEvery == 0 {
			report(live.idle())
		}
	}
}

// failStreak tolerates transient I/O errors (a Wi-Fi roam, a momentarily
// missing route, a full send buffer): each failed packet is just dropped —
// the layers above retransmit — and only a failure that persists for `limit`
// with no success in between is fatal.
type failStreak struct {
	limit time.Duration
	since time.Time
	n     int
}

// fail records one failure and reports whether the streak is now fatal, and
// whether this one is worth logging (the first, then every 200th).
func (f *failStreak) fail(now time.Time) (fatal, log bool) {
	if f.n == 0 {
		f.since = now
	}
	f.n++
	return now.Sub(f.since) > f.limit, f.n == 1 || f.n%200 == 0
}

func (f *failStreak) ok() { f.n = 0 }
