package ike

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func localResponder(t *testing.T, answer bool) (*net.UDPConn, int) {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	go func() {
		buf := make([]byte, 4096)
		for {
			n, from, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if !answer || n < headerLen {
				continue
			}
			// MM2 stand-in: echo the initiator cookie back in a header.
			reply := append([]byte{}, buf[:8]...)
			reply = append(reply, bytes.Repeat([]byte{1}, headerLen-8)...)
			c.WriteToUDP(reply, from)
		}
	}()
	return c, c.LocalAddr().(*net.UDPAddr).Port
}

func TestProbeResponder(t *testing.T) {
	orig := probeWait
	probeWait = 50 * time.Millisecond
	t.Cleanup(func() { probeWait = orig })
	lo := net.ParseIP("127.0.0.1")
	props := []string{"3des-sha1-modp1024"}

	_, answering := localResponder(t, true)
	if err := ProbeResponder(context.Background(), lo, answering, 0, props); err != nil {
		t.Fatalf("answering responder reported as unreachable: %v", err)
	}

	_, silent := localResponder(t, false)
	if err := ProbeResponder(context.Background(), lo, silent, 0, props); !errors.Is(err, ErrNoResponse) {
		t.Fatalf("silent responder: got %v, want ErrNoResponse — silence must never read as reachable", err)
	}

	held, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := ProbeResponder(context.Background(), lo, answering, held.LocalAddr().(*net.UDPAddr).Port, props); !errors.Is(err, ErrPortInUse) {
		t.Fatalf("busy local port: got %v, want ErrPortInUse", err)
	}
}
