package ike

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"
)

// sessionPair is two ends of one IKE SA (same cookies, same Phase 1 keys) on
// loopback, so either can run a Quick Mode against the other.
func sessionPair(t *testing.T) (a, b *Session) {
	t.Helper()
	lo := net.ParseIP("127.0.0.1")
	ca, err := net.ListenUDP("udp4", &net.UDPAddr{IP: lo})
	if err != nil {
		t.Skip("no loopback UDP:", err)
	}
	cb, err := net.ListenUDP("udp4", &net.UDPAddr{IP: lo})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ca.Close(); cb.Close() })
	mk := func(conn, peer *net.UDPConn) *Session {
		s := &Session{
			conn: conn, serverIP: lo, destAddr: peer.LocalAddr().(*net.UDPAddr), floated: true,
			Transform: Transform{Encryption: Enc3DES, Hash: HashSHA1},
			Keys: &Phase1Keys{
				SKEYIDa: bytes.Repeat([]byte{0xA1}, 20),
				SKEYIDd: bytes.Repeat([]byte{0xD1}, 20),
				EncKey:  bytes.Repeat([]byte{0xE1}, 24),
			},
			phase1IV: bytes.Repeat([]byte{0x11}, 8),
		}
		copy(s.InitiatorSPI[:], "initcook")
		copy(s.ResponderSPI[:], "respcook")
		return s
	}
	return mk(ca, cb), mk(cb, ca)
}

// A rekey the peer starts is answered by the same code path a real server
// exercises — and checked against this client's own initiator, which already
// works against a live server: HASH(2), the CBC chain, and both directions'
// KEYMAT must agree for the initiator to accept QM2 and for the responder to
// accept QM3.
func TestServerInitiatedQuickModeAgreesWithInitiator(t *testing.T) {
	server, client := sessionPair(t) // "server" initiates, "client" answers
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got := make(chan *QuickModeResult, 1)
	server.StartDataPhase(ctx, Events{})
	client.StartDataPhase(ctx, Events{
		ESPProposals: []string{"aes128-sha1", "3des-sha1"},
		NewChildSA:   func(r *QuickModeResult) { got <- r },
	})

	lo := net.ParseIP("127.0.0.1")
	srv, err := server.RekeyQuickMode([]string{"3des-sha1", "aes256-sha256"}, lo, lo) // offers one we accept and one we do not
	if err != nil {
		t.Fatalf("server-side Quick Mode did not complete: %v", err)
	}
	var cli *QuickModeResult
	select {
	case cli = <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("client never produced its SA pair")
	}

	if cli.Inbound.SPI != srv.Outbound.SPI || cli.Outbound.SPI != srv.Inbound.SPI {
		t.Fatalf("SPIs do not mirror: client in/out %08x/%08x, server in/out %08x/%08x", cli.Inbound.SPI, cli.Outbound.SPI, srv.Inbound.SPI, srv.Outbound.SPI)
	}
	if !bytes.Equal(cli.Inbound.EncKey, srv.Outbound.EncKey) || !bytes.Equal(cli.Inbound.AuthKey, srv.Outbound.AuthKey) {
		t.Fatal("what the server encrypts with is not what the client decrypts with")
	}
	if !bytes.Equal(cli.Outbound.EncKey, srv.Inbound.EncKey) || !bytes.Equal(cli.Outbound.AuthKey, srv.Inbound.AuthKey) {
		t.Fatal("what the client encrypts with is not what the server decrypts with")
	}
	if cli.Inbound.SPI == 0 || cli.Outbound.SPI == 0 {
		t.Fatal("zero SPI")
	}
	if cli.Inbound.Transform.Encryption != Enc3DES || cli.Inbound.Transform.Hash != HashSHA1 {
		t.Fatalf("client picked %+v, want the only offer it accepts (3des-sha1)", cli.Inbound.Transform)
	}
	if cli.Lifetime != espLifetime {
		t.Fatalf("lifetime %s, want %s", cli.Lifetime, espLifetime)
	}
}

func TestServerInitiatedQuickModeWithNoAcceptableTransformIsRefused(t *testing.T) {
	server, client := sessionPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan *QuickModeResult, 1)
	server.StartDataPhase(ctx, Events{})
	client.StartDataPhase(ctx, Events{ESPProposals: []string{"aes128-sha1"}, NewChildSA: func(r *QuickModeResult) { got <- r }})

	lo := net.ParseIP("127.0.0.1")
	go server.RekeyQuickMode([]string{"3des-sha1"}, lo, lo) // nothing the client accepts; gives up on its own later
	select {
	case <-got:
		t.Fatal("accepted a transform that is not in our configured list")
	case <-time.After(500 * time.Millisecond):
	}
}

func TestServerInitiatedQuickModeIgnoredWhenNotEnabled(t *testing.T) {
	server, client := sessionPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server.StartDataPhase(ctx, Events{})
	client.StartDataPhase(ctx, Events{}) // no ESPProposals/NewChildSA: previous behaviour

	lo := net.ParseIP("127.0.0.1")
	go server.RekeyQuickMode([]string{"3des-sha1"}, lo, lo)
	time.Sleep(300 * time.Millisecond)
	client.dp.respMu.Lock()
	n := len(client.dp.resp)
	client.dp.respMu.Unlock()
	if n != 0 {
		t.Fatal("a disabled responder still started an exchange")
	}
}
