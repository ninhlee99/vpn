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

func TestCheckServerID(t *testing.T) {
	ipv4 := MarshalIPv4ID(net.ParseIP("203.0.113.7"))
	fqdn := MarshalID(IDTypeFQDN, []byte("vpn.example.com"))
	unknown := MarshalID(9, []byte{0xde, 0xad})

	cases := []struct {
		name    string
		want    string
		body    []byte
		wantErr bool
	}{
		{"empty server_id accepts anything", "", unknown, false},
		{"matching IPv4", "203.0.113.7", ipv4, false},
		{"mismatching IPv4", "203.0.113.8", ipv4, true},
		{"matching FQDN", "vpn.example.com", fqdn, false},
		// Previously skipped entirely: only ID_IPV4_ADDR was compared.
		{"mismatching FQDN", "vpn.other.com", fqdn, true},
		{"unknown ID type never matches", "203.0.113.7", unknown, true},
		{"truncated payload", "203.0.113.7", []byte{1, 0}, true},
	}
	for _, c := range cases {
		err := checkServerID(c.want, c.body)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v, wantErr=%v", c.name, err, c.wantErr)
		}
	}
}
