package ike

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// A responder that never answers MM1 from the first local port must cost
// only firstPortMM1Retransmits+1 sends there before Main Mode is retried
// from another port with the full budget — and the error must say MM1 was
// unanswered, which is what gates the fallback.
func TestPhase1FallsBackToAnotherLocalPort(t *testing.T) {
	lo := net.ParseIP("127.0.0.1")
	silent, err := net.ListenUDP("udp4", &net.UDPAddr{IP: lo})
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()

	var mu sync.Mutex
	sends := map[int]int{} // source port -> MM1s received
	go func() {
		buf := make([]byte, 2048)
		for {
			_, from, err := silent.ReadFromUDP(buf)
			if err != nil {
				return
			}
			mu.Lock()
			sends[from.Port]++
			mu.Unlock()
		}
	}()

	// Stand in for 500 with a free unprivileged port.
	first, err := net.ListenUDP("udp4", &net.UDPAddr{IP: lo})
	if err != nil {
		t.Fatal(err)
	}
	firstPort := first.LocalAddr().(*net.UDPAddr).Port
	first.Close()

	origPorts, origInterval := phase1LocalPorts, retransmitInterval
	phase1LocalPorts, retransmitInterval = []int{firstPort, 0}, 30*time.Millisecond
	t.Cleanup(func() { phase1LocalPorts, retransmitInterval = origPorts, origInterval })

	serverPort := silent.LocalAddr().(*net.UDPAddr).Port
	_, err = establishPhase1To(context.Background(), Config{
		ServerHost: "127.0.0.1", PSK: "x", Proposals: []string{"3des-sha1-modp1024"}, LocalIP: lo,
	}, serverPort)
	if err == nil {
		t.Fatal("Phase 1 against a silent responder succeeded")
	}
	if !errors.Is(err, errMM1Unanswered) {
		t.Fatalf("error does not mark MM1 as unanswered: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if got := sends[firstPort]; got != firstPortMM1Retransmits+1 {
		t.Fatalf("first port sent %d MM1s, want %d", got, firstPortMM1Retransmits+1)
	}
	others := 0
	for p, n := range sends {
		if p != firstPort {
			others++
			if n != maxRetransmits+1 {
				t.Fatalf("fallback port sent %d MM1s, want the full %d", n, maxRetransmits+1)
			}
		}
	}
	if others != 1 {
		t.Fatalf("expected exactly one fallback port, saw ports %v", sends)
	}
}
