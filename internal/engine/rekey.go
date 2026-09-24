package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"vpn/internal/ike"
	"vpn/internal/ipsec"
	"vpn/internal/vpnlog"
)

// saPair is one negotiated ESP SA pair and when it stops being valid.
type saPair struct {
	in, out *ipsec.SA
	expires time.Time
}

// saSet holds every ESP SA pair the tunnel may still receive on. Outbound
// traffic always uses the newest pair; inbound is matched by the SPI on
// each packet, so packets the server still sends on the previous pair
// right after a rekey are not lost. L2TP and PPP sit above this and never
// notice a rekey — no re-authentication, no session interruption.
type saSet struct {
	mu    sync.RWMutex
	pairs []saPair // oldest first; the last one is current
}

func newSASet(qm *ike.QuickModeResult) (*saSet, error) {
	s := &saSet{}
	if err := s.install(qm, time.Now()); err != nil {
		return nil, err
	}
	return s, nil
}

// install makes qm's pair current. Older pairs stay for inbound until the
// server deletes them or they expire.
func (s *saSet) install(qm *ike.QuickModeResult, now time.Time) error {
	out, err := newESPSA(qm.Outbound)
	if err != nil {
		return fmt.Errorf("outbound ESP SA: %w", err)
	}
	in, err := newESPSA(qm.Inbound)
	if err != nil {
		return fmt.Errorf("inbound ESP SA: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pairs = append(s.pairs, saPair{in: in, out: out, expires: now.Add(qm.Lifetime)})
	return nil
}

func (s *saSet) current() saPair {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pairs[len(s.pairs)-1]
}

// inbound returns the SA a packet with this SPI belongs to, or nil.
func (s *saSet) inbound(spi uint32) *ipsec.SA {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.pairs) - 1; i >= 0; i-- {
		if s.pairs[i].in.SPI == spi {
			return s.pairs[i].in
		}
	}
	return nil
}

// drop removes the non-current pairs the peer deleted. A Delete names
// either direction's SPI depending on the implementation, so both are
// matched. The current pair is kept even if named: without it nothing can
// be sent at all, and the next rekey replaces it anyway.
func (s *saSet) drop(spis []uint32) (droppedCurrent bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	named := func(p saPair) bool {
		for _, spi := range spis {
			if p.in.SPI == spi || p.out.SPI == spi {
				return true
			}
		}
		return false
	}
	kept := s.pairs[:0]
	last := len(s.pairs) - 1
	for i, p := range s.pairs {
		if named(p) {
			if i == last {
				droppedCurrent = true
			} else {
				continue
			}
		}
		kept = append(kept, p)
	}
	s.pairs = kept
	return droppedCurrent
}

// expireOld removes non-current pairs past their lifetime.
func (s *saSet) expireOld(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.pairs[:0]
	last := len(s.pairs) - 1
	for i, p := range s.pairs {
		if i == last || now.Before(p.expires) {
			kept = append(kept, p)
		}
	}
	s.pairs = kept
}

// Rekey timing. The client replaces the pair once half its lifetime has
// passed: with strongSwan's defaults (margintime 9m, rekeyfuzz 100%) the
// server would start its own rekey no earlier than lifetime-18m, and this
// client cannot answer a server-initiated Quick Mode — so it must always
// get there first. Failures retry well before the pair actually expires.
const (
	rekeyFraction   = 2 // rekey at lifetime/rekeyFraction
	minRekeyWait    = 30 * time.Second
	rekeyRetryEvery = 30 * time.Second
)

// runRekey keeps the tunnel's SA pair fresh for as long as ctx lives.
// override > 0 replaces the lifetime-based schedule (for testing a rekey
// without waiting half an hour).
func runRekey(ctx context.Context, sas *saSet, sess *ike.Session, rekey func() (*ike.QuickModeResult, error), lifetime, override time.Duration) {
	schedule := func(l time.Duration) time.Duration {
		if override > 0 {
			return override
		}
		return rekeyDelay(l)
	}
	next := schedule(lifetime)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(next):
		}
		sas.expireOld(time.Now())
		if sess.Lifetime > 0 && time.Since(sess.EstablishedAt) > sess.Lifetime {
			vpnlog.Error("ENGINE", "IKE SA lifetime has elapsed — the server may refuse this rekey", vpnlog.Fields{
				"ike_lifetime_s": int(sess.Lifetime / time.Second),
			})
		}
		qm, err := rekey()
		if err == nil {
			err = sas.install(qm, time.Now())
		}
		if err != nil {
			remaining := time.Until(sas.current().expires)
			vpnlog.Error("ENGINE", "ESP rekey failed — retrying", vpnlog.Fields{
				"err": err, "current_sa_remaining_s": int(remaining / time.Second),
			})
			next = rekeyRetryEvery
			continue
		}
		vpnlog.Info("ENGINE", "ESP SA rekeyed", vpnlog.Fields{
			"in_spi": fmt.Sprintf("%08x", qm.Inbound.SPI), "out_spi": fmt.Sprintf("%08x", qm.Outbound.SPI),
			"lifetime_s": int(qm.Lifetime / time.Second),
		})
		next = schedule(qm.Lifetime)
	}
}

func rekeyDelay(lifetime time.Duration) time.Duration {
	d := lifetime / rekeyFraction
	if d < minRekeyWait {
		d = minRekeyWait
	}
	return d
}
