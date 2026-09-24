package engine

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"syscall"

	"vpn/internal/ike"
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
	sess        *ike.Session
	sas         *saSet // current pair for sending; every live pair for receiving (see rekey.go)
	repairRoute func() error
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
	// Checksum remains zero. RFC 3948 §3.1.2 permits this for integrity-
	// protected UDP transported by ESP: NAT changes the IP addresses used by
	// a non-zero checksum's pseudo-header and cannot adjust encrypted ESP.
	payload := append(udpHdr, l2tpMsg...)

	pkt, err := t.sas.current().out.Encrypt(payload, protoUDP)
	if err != nil {
		return fmt.Errorf("ESP encrypt: %w", err)
	}
	return sendWithRouteRetry(func() error { return t.sess.SendESP(pkt) }, t.repairRoute)
}

// sendWithRouteRetry repairs a route lost by macOS's route reconciler and
// retries once. EHOSTUNREACH/ENETUNREACH means the kernel did not transmit
// the datagram, so the retry cannot duplicate an ESP packet.
func sendWithRouteRetry(send func() error, repairRoute func() error) error {
	err := send()
	if err == nil || repairRoute == nil || (!errors.Is(err, syscall.EHOSTUNREACH) && !errors.Is(err, syscall.ENETUNREACH)) {
		return err
	}
	if repairErr := repairRoute(); repairErr != nil {
		return fmt.Errorf("repair VPN server route after %w: %v", err, repairErr)
	}
	return send()
}

func (t *espTransport) Recv(ctx context.Context) ([]byte, error) {
	for {
		pkt, err := t.sess.RecvESP(ctx)
		if err != nil {
			return nil, err
		}
		if len(pkt) < 4 {
			continue
		}
		in := t.sas.inbound(binary.BigEndian.Uint32(pkt[0:4]))
		if in == nil {
			continue // an SA already deleted/expired, or not ours
		}
		payload, nextHeader, err := in.Decrypt(pkt)
		if err != nil {
			// A stray/replayed/corrupt ESP packet is not fatal to the
			// session — log and keep waiting rather than aborting the
			// whole tunnel over one bad datagram.
			continue
		}
		// Never log the decrypted bytes themselves: this path carries the
		// MS-CHAPv2 exchange and every tunnelled user packet in cleartext.
		vpnlog.Debug("ENGINE", "ESP decrypted", vpnlog.Fields{
			"next_header": nextHeader, "payload_len": len(payload),
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
