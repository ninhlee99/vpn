package ike

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"
)

func fakeClientConfig(psk string) Config {
	return Config{
		ServerHost: "127.0.0.1", PSK: psk, LocalIP: net.ParseIP("127.0.0.1"),
		Proposals: []string{"aes128-sha1-modp1024"},
	}
}

// point the client's fixed ports at the fake server for one test.
func useFakePorts(t *testing.T, f *fakeServer, localNATT int) {
	sp, lp, pp := natTServerPort, natTLocalPort, phase1LocalPorts
	natTServerPort, natTLocalPort, phase1LocalPorts = f.nattPort(), localNATT, []int{0}
	t.Cleanup(func() { natTServerPort, natTLocalPort, phase1LocalPorts = sp, lp, pp })
}

func TestPhase1AndQuickModeAgainstFakeServer(t *testing.T) {
	f := newFakeServer(t, "sekrit", []string{"aes128-sha1"})
	useFakePorts(t, f, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	sess, err := establishPhase1To(ctx, fakeClientConfig("sekrit"), f.mainPort())
	if err != nil {
		t.Fatalf("Phase 1: %v", err)
	}
	defer sess.Close()
	if !sess.NATDetected || !sess.floated {
		t.Fatalf("expected NAT-T float (detected=%v floated=%v)", sess.NATDetected, sess.floated)
	}
	lo := net.ParseIP("127.0.0.1")
	qm, err := sess.EstablishQuickMode([]string{"aes128-sha1"}, lo, lo)
	if err != nil {
		t.Fatalf("Quick Mode: %v", err)
	}
	select {
	case srv := <-f.newSA:
		if srv.Inbound.SPI != qm.Outbound.SPI || !bytes.Equal(srv.Inbound.EncKey, qm.Outbound.EncKey) {
			t.Fatal("client and server disagree on the SA pair")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server never completed its side of Quick Mode")
	}
}

func TestPhase1WrongPSKFails(t *testing.T) {
	f := newFakeServer(t, "sekrit", []string{"aes128-sha1"})
	f.expectAuthFailure()
	useFakePorts(t, f, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if _, err := establishPhase1To(ctx, fakeClientConfig("not-the-psk"), f.mainPort()); err == nil {
		t.Fatal("Phase 1 succeeded with the wrong pre-shared key")
	}
}

// A second IKE SA built while the first is still carrying traffic — the heart
// of re-authentication — must not disturb the first, must find its own local
// port, and both must keep working (each can run a Quick Mode).
func TestSecondPhase1WhileFirstIsLive(t *testing.T) {
	f := newFakeServer(t, "sekrit", []string{"aes128-sha1"})
	useFakePorts(t, f, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	lo := net.ParseIP("127.0.0.1")

	first, err := establishPhase1To(ctx, fakeClientConfig("sekrit"), f.mainPort())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := first.EstablishQuickMode([]string{"aes128-sha1"}, lo, lo); err != nil {
		t.Fatal(err)
	}
	<-f.newSA
	first.StartDataPhase(ctx, Events{})
	firstPort := first.conn.LocalAddr().(*net.UDPAddr).Port

	// Now local NAT-T port "4500" is taken by the first session.
	natTLocalPort = firstPort

	plain := fakeClientConfig("sekrit")
	if _, err := establishPhase1To(ctx, plain, f.mainPort()); err == nil {
		t.Fatal("an ordinary Phase 1 must not silently take another port: the port clash should surface")
	}

	re := fakeClientConfig("sekrit")
	re.Reauth = true
	second, err := establishPhase1To(ctx, re, f.mainPort())
	if err != nil {
		t.Fatalf("re-authentication Phase 1: %v", err)
	}
	defer second.Close()
	if second.conn.LocalAddr().(*net.UDPAddr).Port == firstPort {
		t.Fatal("second session shares the first one's local port")
	}
	if _, err := second.EstablishQuickMode([]string{"aes128-sha1"}, lo, lo); err != nil {
		t.Fatalf("Quick Mode on the new IKE SA: %v", err)
	}
	<-f.newSA

	// The first session is untouched and still rekeys.
	if _, err := first.RekeyQuickMode([]string{"aes128-sha1"}, lo, lo); err != nil {
		t.Fatalf("the original IKE SA broke when the second one was built: %v", err)
	}
	<-f.newSA
}
