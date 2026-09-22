package engine

import (
	"context"
	"encoding/binary"
	"fmt"

	"vpn/internal/ike"
	"vpn/internal/ipsec"
	"vpn/internal/l2tp"
	"vpn/internal/ppp"
	"vpn/internal/vpnlog"
)

// espTransport implements l2tp.Transport over an ESP SA pair carried inside
// the IKE session's already-floated NAT-T socket. It reconstructs the inner
// UDP/1701 header ESP transport mode protects (RFC 4303 §3.1: the payload
// ESP encrypts is the original packet's next header onward — here, a UDP
// header plus the L2TP message) since this client never builds real IP
// packets for its own control/data traffic, only the payload IPsec expects.
type espTransport struct {
	sess *ike.Session
	out  *ipsec.SA
	in   *ipsec.SA
}

const (
	l2tpPort = 1701
	protoUDP = 17
)

func (t *espTransport) Send(l2tpMsg []byte) error {
	udpHdr := make([]byte, 8)
	binary.BigEndian.PutUint16(udpHdr[0:2], l2tpPort) // src port
	binary.BigEndian.PutUint16(udpHdr[2:4], l2tpPort) // dst port
	binary.BigEndian.PutUint16(udpHdr[4:6], uint16(8+len(l2tpMsg)))
	// checksum (udpHdr[6:8]) left 0 — optional for IPv4 UDP (RFC 768), and
	// the payload is already integrity-protected by ESP's own ICV.
	payload := append(udpHdr, l2tpMsg...)

	pkt, err := t.out.Encrypt(payload, protoUDP)
	if err != nil {
		return fmt.Errorf("ESP encrypt: %w", err)
	}
	return t.sess.SendESP(pkt)
}

func (t *espTransport) Recv(ctx context.Context) ([]byte, error) {
	for {
		pkt, err := t.sess.RecvESP(ctx)
		if err != nil {
			return nil, err
		}
		payload, nextHeader, err := t.in.Decrypt(pkt)
		if err != nil {
			// A stray/replayed/corrupt ESP packet is not fatal to the
			// session — log and keep waiting rather than aborting the
			// whole tunnel over one bad datagram.
			continue
		}
		vpnlog.Debug("ENGINE", "ESP decrypted", vpnlog.Fields{
			"next_header": nextHeader, "payload_len": len(payload), "hex": fmt.Sprintf("%x", payload),
		})
		if nextHeader != protoUDP || len(payload) < 8 {
			continue
		}
		return payload[8:], nil // strip the inner UDP header, keep the L2TP message
	}
}

// pppOverL2TP implements ppp.Transport over an established l2tp.Tunnel's
// data channel.
type pppOverL2TP struct {
	tun *l2tp.Tunnel
}

func (p *pppOverL2TP) SendFrame(protocol uint16, payload []byte) error {
	frame := ppp.Frame{Protocol: protocol, Payload: payload}
	return p.tun.SendData(frame.Marshal())
}

func (p *pppOverL2TP) RecvFrame(ctx context.Context) (uint16, []byte, error) {
	raw, err := p.tun.RecvData(ctx)
	if err != nil {
		return 0, nil, err
	}
	f, err := ppp.Parse(raw)
	if err != nil {
		return 0, nil, err
	}
	return f.Protocol, f.Payload, nil
}
