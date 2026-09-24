package ike

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestSendNATKeepaliveOnlyWhenFloated(t *testing.T) {
	srv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Skip("no loopback UDP:", err)
	}
	defer srv.Close()
	cli, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	s := &Session{conn: cli, destAddr: srv.LocalAddr().(*net.UDPAddr)}

	if err := s.SendNATKeepalive(); err != nil {
		t.Fatal(err)
	}
	srv.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, _, err := srv.ReadFromUDP(make([]byte, 16)); err == nil {
		t.Fatal("sent a NAT keepalive although no NAT was detected")
	}

	s.floated = true
	if err := s.SendNATKeepalive(); err != nil {
		t.Fatal(err)
	}
	srv.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 16)
	n, _, err := srv.ReadFromUDP(buf)
	if err != nil || n != 1 || buf[0] != 0xFF {
		t.Fatalf("want one 0xFF byte (RFC 3948 §2.3), got n=%d err=%v", n, err)
	}
}

func TestRecvESPDataPhaseWaitsForPacketsWithoutIdleError(t *testing.T) {
	s := &Session{dp: &dataPlane{espIn: make(chan []byte, 1), done: make(chan struct{})}}
	go func() {
		time.Sleep(150 * time.Millisecond)
		s.dp.espIn <- []byte{1, 2, 3, 4}
	}()
	pkt, err := s.RecvESP(context.Background())
	if err != nil || len(pkt) != 4 {
		t.Fatalf("a quiet tunnel must keep waiting, got %v %v", pkt, err)
	}
}

func TestReaderBlocksIdleAndStopsPromptlyOnCancel(t *testing.T) {
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Skip("no loopback UDP:", err)
	}
	defer c.Close()
	// A deadline left over from negotiation must not make the idle reader spin or fail.
	c.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	s := &Session{conn: c, serverIP: net.IPv4(127, 0, 0, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	s.StartDataPhase(ctx, Events{})
	time.Sleep(100 * time.Millisecond)
	select {
	case <-s.dp.done:
		t.Fatal("reader exited on its own while idle")
	default:
	}
	cancel()
	select {
	case <-s.dp.done:
	case <-time.After(time.Second):
		t.Fatal("reader did not stop after ctx cancel")
	}
}
