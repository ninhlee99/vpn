package engine

import (
	"context"
	"testing"
	"time"
)

func TestWatchdogKeepsProbingWhileTrafficArrives(t *testing.T) {
	live := newLiveness()
	ctx, cancel := context.WithCancel(context.Background())
	probes := 0
	done := make(chan error, 1)
	go func() {
		done <- watchdog(ctx, 10*time.Millisecond, 200*time.Millisecond, live, func() { probes++; live.touch() }, func(time.Duration) {})
	}()
	time.Sleep(120 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("a peer that keeps answering was declared dead: %v", err)
	}
	if probes < 3 {
		t.Fatalf("only %d probes sent", probes)
	}
}

func TestWatchdogDeclaresSilentPeerDead(t *testing.T) {
	live := newLiveness()
	err := watchdog(context.Background(), 10*time.Millisecond, 60*time.Millisecond, live, func() {}, func(time.Duration) {})
	if err == nil {
		t.Fatal("a peer that never answers must be declared dead")
	}
}

func TestFailStreakOnlyFatalWhenPersistent(t *testing.T) {
	f := &failStreak{limit: time.Second}
	now := time.Now()
	if fatal, logIt := f.fail(now); fatal || !logIt {
		t.Fatalf("first failure: fatal=%v log=%v, want tolerated and logged", fatal, logIt)
	}
	if fatal, _ := f.fail(now.Add(900 * time.Millisecond)); fatal {
		t.Fatal("a short burst of errors must not kill the tunnel")
	}
	f.ok()
	if fatal, _ := f.fail(now.Add(5 * time.Second)); fatal {
		t.Fatal("a success in between must reset the streak")
	}
	if fatal, _ := f.fail(now.Add(7 * time.Second)); !fatal {
		t.Fatal("errors persisting past the limit must be fatal")
	}
}
