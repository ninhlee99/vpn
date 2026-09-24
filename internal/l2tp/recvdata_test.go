package l2tp

import (
	"context"
	"testing"
	"time"
)

type scriptedTransport struct {
	in   chan []byte
	sent [][]byte
}

func (s *scriptedTransport) Send(m []byte) error { s.sent = append(s.sent, m); return nil }
func (s *scriptedTransport) Recv(ctx context.Context) ([]byte, error) {
	select {
	case m := <-s.in:
		return m, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Traffic of a stale session (another tunnel or session ID) arrives on the
// same IPsec SA after a reconnect; only frames addressed to OUR tunnel and
// session may reach PPP.
func TestRecvDataDeliversOnlyOurSession(t *testing.T) {
	tr := &scriptedTransport{in: make(chan []byte, 8)}
	tun := &Tunnel{t: tr, localTunnelID: 0x08ce, localSessionID: 1}

	stale := MarshalData(0x82d7, 1, []byte{0xff, 0x03, 0x00, 0x21, 'i', 'p'})    // other tunnel
	otherSession := MarshalData(0x08ce, 7, []byte{0xff, 0x03, 0xc0, 0x21, 0x05}) // our tunnel, other session (a Terminate!)
	ours := MarshalData(0x08ce, 1, []byte{0xff, 0x03, 0xc0, 0x23, 'c', 'h', 'a', 'p'})
	tr.in <- stale
	tr.in <- otherSession
	tr.in <- ours

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := tun.RecvData(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(got[len(got)-4:]) != "chap" {
		t.Fatalf("a foreign session's frame reached PPP: %x", got)
	}
	if tun.strayData != 2 {
		t.Fatalf("dropped %d foreign messages, want 2", tun.strayData)
	}
}
