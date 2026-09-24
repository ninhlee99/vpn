package engine

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vpn/internal/ike"
)

type fakeIKE struct {
	name      string
	lifetime  time.Duration
	born      time.Time
	in        chan []byte
	closed    atomic.Bool
	keepalive atomic.Int32
	mu        sync.Mutex
	sent      [][]byte
}

func newFakeIKE(name string, lifetime time.Duration) *fakeIKE {
	return &fakeIKE{name: name, lifetime: lifetime, born: time.Now(), in: make(chan []byte, 8)}
}

func (f *fakeIKE) SendESP(p []byte) error {
	f.mu.Lock()
	f.sent = append(f.sent, p)
	f.mu.Unlock()
	return nil
}
func (f *fakeIKE) RecvESP(ctx context.Context) ([]byte, error) {
	select {
	case p, ok := <-f.in:
		if !ok {
			return nil, errors.New("closed")
		}
		return p, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (f *fakeIKE) SendNATKeepalive() error { f.keepalive.Add(1); return nil }
func (f *fakeIKE) RekeyQuickMode([]string, net.IP, net.IP) (*ike.QuickModeResult, error) {
	return nil, errors.New("not used")
}
func (f *fakeIKE) Close() {
	if f.closed.CompareAndSwap(false, true) {
		close(f.in)
	}
}
func (f *fakeIKE) Age() time.Duration         { return time.Since(f.born) }
func (f *fakeIKE) IKELifetime() time.Duration { return f.lifetime }
func (f *fakeIKE) sentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func fastReauth(t *testing.T) {
	a, b, c, d := reauthMinWait, reauthRetry, reauthGrace, reauthNum
	reauthMinWait, reauthRetry, reauthGrace = 5*time.Millisecond, 30*time.Millisecond, 40*time.Millisecond
	t.Cleanup(func() { reauthMinWait, reauthRetry, reauthGrace, reauthNum = a, b, c, d })
}

// runReauthT runs runReauth and, at test end, stops it and waits for it —
// it reads the package timing vars, which fastReauth restores afterwards.
func runReauthT(t *testing.T, ctx context.Context, cancel context.CancelFunc, m *ikeSessions, sas *saSet,
	establish func(context.Context) (ikeSession, *ike.QuickModeResult, error), giveUp func(error)) {
	done := make(chan struct{})
	go func() { defer close(done); runReauth(ctx, m, sas, establish, giveUp) }()
	t.Cleanup(func() { cancel(); <-done })
}

func TestReauthDelay(t *testing.T) {
	if _, ok := reauthDelay(0, 0); ok {
		t.Fatal("an unknown lifetime must disable re-authentication")
	}
	if w, _ := reauthDelay(4*time.Hour, 0); w != 3*time.Hour {
		t.Fatalf("fresh 4h IKE SA: %s, want 3h (three quarters of its life)", w)
	}
	if w, _ := reauthDelay(4*time.Hour, 3*time.Hour+50*time.Minute); w != reauthMinWait {
		t.Fatalf("an IKE SA past the point is re-authed at once (floor %s), got %s", reauthMinWait, w)
	}
}

func TestMuxSendsOnNewestAndReceivesFromAll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newIKESessions(ctx)
	oldS, newS := newFakeIKE("old", time.Hour), newFakeIKE("new", time.Hour)
	m.add(oldS)
	m.add(newS)

	m.sendESP([]byte{1})
	if oldS.sentCount() != 0 || newS.sentCount() != 1 {
		t.Fatalf("outbound must leave on the newest session (old=%d new=%d)", oldS.sentCount(), newS.sentCount())
	}
	oldS.in <- []byte("from-old")
	newS.in <- []byte("from-new")
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		rctx, rc := context.WithTimeout(ctx, time.Second)
		p, err := m.recv(rctx)
		rc()
		if err != nil {
			t.Fatal(err)
		}
		got[string(p)] = true
	}
	if !got["from-old"] || !got["from-new"] {
		t.Fatalf("inbound must merge every live session, got %v", got)
	}

	m.keepalive()
	if oldS.keepalive.Load() != 1 || newS.keepalive.Load() != 1 {
		t.Fatal("NAT keepalives must keep every session's mapping alive")
	}

	m.retire(oldS)
	if !oldS.closed.Load() || newS.closed.Load() {
		t.Fatal("retiring must close exactly the old session")
	}
	m.retire(newS)
	if newS.closed.Load() {
		t.Fatal("the last session must never be retired")
	}
}

func TestReauthSwitchesSessionAndPairThenRetiresOldAfterGrace(t *testing.T) {
	fastReauth(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sas, _ := newSASet(testQM(0x1, 0xA, time.Hour))
	m := newIKESessions(ctx)
	oldS := newFakeIKE("old", 60*time.Millisecond) // due at 3/4 of that
	m.add(oldS)

	newS := newFakeIKE("new", time.Hour) // long enough that it is not re-authed again in this test
	calls := atomic.Int32{}
	establish := func(context.Context) (ikeSession, *ike.QuickModeResult, error) {
		calls.Add(1)
		return newS, testQM(0x2, 0xB, time.Hour), nil
	}
	giveUp := func(err error) { t.Errorf("gave up although re-authentication works: %v", err) }
	runReauthT(t, ctx, cancel, m, sas, establish, giveUp)

	deadline := time.After(2 * time.Second)
	for m.current() != ikeSession(newS) {
		select {
		case <-deadline:
			t.Fatal("never switched to the new IKE SA")
		case <-time.After(5 * time.Millisecond):
		}
	}
	// The session moves first and the pair right after (see installReauthed).
	for sas.current().out.SPI != 0xB {
		select {
		case <-deadline:
			t.Fatalf("still sending on SA %x, want the new pair's b", sas.current().out.SPI)
		case <-time.After(time.Millisecond):
		}
	}
	if sas.inbound(0x1) == nil {
		t.Fatal("the old pair must still be accepted inbound during the overlap")
	}
	if oldS.closed.Load() {
		t.Fatal("the old IKE SA was closed before the grace period")
	}
	for !oldS.closed.Load() {
		select {
		case <-deadline:
			t.Fatal("old IKE SA never retired")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("established %d times, want once", n)
	}
}

func TestReauthRetriesThenGivesUpOnceIKEExpired(t *testing.T) {
	fastReauth(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sas, _ := newSASet(testQM(0x1, 0xA, time.Hour))
	m := newIKESessions(ctx)
	m.add(newFakeIKE("old", 60*time.Millisecond))

	attempts := atomic.Int32{}
	establish := func(context.Context) (ikeSession, *ike.QuickModeResult, error) {
		attempts.Add(1)
		return nil, nil, errors.New("server refused")
	}
	gave := make(chan error, 1)
	runReauthT(t, ctx, cancel, m, sas, establish, func(err error) { gave <- err })
	select {
	case err := <-gave:
		if attempts.Load() < 1 {
			t.Fatal("gave up without trying")
		}
		if err == nil {
			t.Fatal("nil give-up error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("never gave up although the IKE SA outlived its lifetime")
	}
}

func TestReauthDisabledWhenLifetimeUnknown(t *testing.T) {
	fastReauth(t)
	ctx, cancel := context.WithCancel(context.Background())
	sas, _ := newSASet(testQM(0x1, 0xA, time.Hour))
	m := newIKESessions(ctx)
	m.add(newFakeIKE("old", 0))
	called := atomic.Bool{}
	done := make(chan struct{})
	go func() {
		runReauth(ctx, m, sas, func(context.Context) (ikeSession, *ike.QuickModeResult, error) {
			called.Store(true)
			return nil, nil, errors.New("x")
		}, func(error) {})
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	if called.Load() {
		t.Fatal("re-authenticated an IKE SA with no known lifetime")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runReauth did not stop with its context")
	}
}
