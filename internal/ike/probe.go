package ike

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"time"
)

// Probe timing: probeSends sends, each waiting probeWait for an answer
// (a variable only so tests can shorten it).
const probeSends = 2

var probeWait = 2 * time.Second

// ErrPortInUse reports that the requested local port is already bound.
var ErrPortInUse = errors.New("local port in use")

// ProbeResponder sends a Main Mode MM1 — proposals only, no secret — from
// localPort (0 = any) to server:serverPort and reports whether the responder
// answered with this probe's cookie. Unlike a bare UDP send, which cannot
// tell "open" from "silently dropped", only a real IKE responder answers
// this. serverPort 4500 is framed with the non-ESP marker (RFC 3948). The
// responder keeps a half-open state for the probe until it times out, as it
// does for any client that gives up after MM2.
func ProbeResponder(ctx context.Context, server net.IP, serverPort, localPort int, proposals []string) error {
	transforms := make([]Transform, 0, len(proposals))
	for _, p := range proposals {
		t, err := ParseProposal(p)
		if err != nil {
			return err
		}
		transforms = append(transforms, t)
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: localPort})
	if err != nil {
		if isAddrInUse(err) {
			return fmt.Errorf("%w: UDP/%d", ErrPortInUse, localPort)
		}
		return err
	}
	defer conn.Close()

	var cookie [8]byte
	if _, err := rand.Read(cookie[:]); err != nil {
		return err
	}
	msg := buildMM1(cookie, MarshalSA(transforms))
	if serverPort == 4500 {
		msg = append(append([]byte{}, nonESPMarker...), msg...)
	}
	dest := &net.UDPAddr{IP: server, Port: serverPort}
	buf := make([]byte, 4096)
	for i := 0; i < probeSends; i++ {
		if _, err := conn.WriteToUDP(msg, dest); err != nil {
			return err
		}
		deadline := time.Now().Add(probeWait)
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			_ = conn.SetReadDeadline(deadline)
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				break // timeout: send again
			}
			got := buf[:n]
			if bytes.HasPrefix(got, nonESPMarker) {
				got = got[len(nonESPMarker):]
			}
			if from.IP.Equal(server) && len(got) >= headerLen && bytes.Equal(got[:8], cookie[:]) {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: no answer to MM1 on UDP/%d from local port %d", ErrNoResponse, serverPort, conn.LocalAddr().(*net.UDPAddr).Port)
}
