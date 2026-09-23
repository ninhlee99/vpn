package ike

import (
	"net"
	"testing"
)

func TestFloatToNATTReplacesSocketAndDestination(t *testing.T) {
	localIP := net.ParseIP("127.0.0.1")
	oldConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: localIP})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { oldConn.Close() })

	s := &Session{
		conn:     oldConn,
		serverIP: net.ParseIP("192.0.2.1"),
		destAddr: &net.UDPAddr{IP: net.ParseIP("192.0.2.1"), Port: 500},
	}
	if err := s.floatToNATT(localIP); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.conn.Close() })

	if got := s.conn.LocalAddr().(*net.UDPAddr).Port; got != 4500 {
		t.Fatalf("NAT-T local port = %d, want 4500", got)
	}
	if got := s.destAddr.Port; got != 4500 {
		t.Fatalf("NAT-T destination port = %d, want 4500", got)
	}
	if !s.floated {
		t.Fatal("floated = false, want true")
	}
	if _, err := oldConn.WriteToUDP([]byte("closed"), s.destAddr); err == nil {
		t.Fatal("pre-NAT-T socket remains usable")
	}
}
