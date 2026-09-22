package ppp

import (
	"context"
	"fmt"
	"time"

	"github.com/ninhlee99/vpn-l2tp/internal/vpnlog"
)

const stage = "PPP"

// Transport is the minimum a PPP session needs from whatever carries its
// frames — the L2TP data channel, in this client. Kept minimal so the PPP
// state machine has no dependency on L2TP/UDP details.
type Transport interface {
	SendFrame(protocol uint16, payload []byte) error
	// RecvFrame blocks until a frame arrives or ctx is done.
	RecvFrame(ctx context.Context) (protocol uint16, payload []byte, err error)
}

// Config is what the engine supplies to bring up one PPP session.
type Config struct {
	MRU      uint16
	Username string
	Password string
	Timeout  time.Duration
}

// Result is everything the engine needs once PPP reaches the OPENED state.
type Result struct {
	IPCP NegotiatedIPCP
}

// Run drives DEAD -> ESTABLISH (LCP) -> AUTHENTICATE (MS-CHAPv2) ->
// NETWORK (IPCP) -> OPENED, per RFC 1661 §3's phase diagram. It returns
// once the link is fully usable, or an error identifying which phase
// failed (LCP_FAILED / PPP_AUTH_FAILURE / IPCP_FAILURE, matching the
// goal's diagnostic vocabulary).
func Run(ctx context.Context, t Transport, cfg Config) (*Result, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	lcp, err := NewLCPConfig(cfg.MRU)
	if err != nil {
		return nil, err
	}
	if err := runLCP(ctx, t, lcp); err != nil {
		return nil, fmt.Errorf("LCP_FAILED: %w", err)
	}
	vpnlog.Info(stage, "LCP established", vpnlog.Fields{"mru": cfg.MRU})

	if err := runAuth(ctx, t, cfg.Username, cfg.Password); err != nil {
		return nil, fmt.Errorf("PPP_AUTH_FAILURE: %w", err)
	}
	vpnlog.Info(stage, "MS-CHAPv2 authentication succeeded", nil)

	ipcp, err := runIPCP(ctx, t)
	if err != nil {
		return nil, fmt.Errorf("IPCP_FAILURE: %w", err)
	}
	vpnlog.Info(stage, "IPCP established", vpnlog.Fields{"local_ip": ipcp.LocalIP.String()})

	return &Result{IPCP: *ipcp}, nil
}

// negotiatePhase runs one side-agnostic RFC 1661 §4 option-negotiation loop
// for a given protocol (LCP or IPCP): send our Configure-Request, and
// process whatever the peer sends (their Configure-Request, our
// Ack/Nak/Reject, their Ack for ours) until both directions are
// acknowledged or ctx expires. buildRequest/handlePeerRequest let LCP and
// IPCP share this loop despite having different option semantics.
func negotiatePhase(
	ctx context.Context,
	t Transport,
	protocol uint16,
	buildRequestOptions func() []Option,
	handlePeerConfigureRequest func(opts []Option) (ackOpts []Option, ok bool),
) error {
	var ourID uint8 = 1
	ourAcked := false
	peerAcked := false

	send := func() error {
		pkt := ControlPacket{Code: CodeConfigureRequest, Identifier: ourID, Data: MarshalOptions(buildRequestOptions())}
		return t.SendFrame(protocol, pkt.Marshal())
	}
	if err := send(); err != nil {
		return err
	}

	retransmit := time.NewTicker(3 * time.Second)
	defer retransmit.Stop()

	for !ourAcked || !peerAcked {
		frameCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		proto, payload, err := t.RecvFrame(frameCtx)
		cancel()
		if err != nil {
			select {
			case <-ctx.Done():
				return fmt.Errorf("timed out waiting for peer (our_acked=%v peer_acked=%v)", ourAcked, peerAcked)
			default:
			}
			if err := send(); err != nil { // retransmit our request
				return err
			}
			continue
		}
		if proto != protocol {
			continue // not this phase's protocol — ignore (e.g. stray LCP Echo during IPCP)
		}
		pkt, err := ParseControlPacket(payload)
		if err != nil {
			return err
		}
		switch pkt.Code {
		case CodeConfigureRequest:
			opts, err := ParseOptions(pkt.Data)
			if err != nil {
				return err
			}
			ackOpts, ok := handlePeerConfigureRequest(opts)
			code := uint8(CodeConfigureAck)
			respData := MarshalOptions(ackOpts)
			if !ok {
				code = CodeConfigureReject
				respData = pkt.Data
			}
			resp := ControlPacket{Code: code, Identifier: pkt.Identifier, Data: respData}
			if err := t.SendFrame(protocol, resp.Marshal()); err != nil {
				return err
			}
			if ok {
				peerAcked = true
			}
		case CodeConfigureAck:
			if pkt.Identifier == ourID {
				ourAcked = true
			}
		case CodeConfigureNak, CodeConfigureReject:
			// Accept whatever the peer countered with on the next attempt —
			// every option this client sends (MRU, Magic-Number / IP 0.0.0.0,
			// DNS 0.0.0.0) is safe to drop entirely if rejected.
			ourID++
			if err := send(); err != nil {
				return err
			}
		case CodeEchoRequest:
			// LCP keepalive from the peer — reply so it doesn't tear down
			// the link thinking we vanished.
			reply := ControlPacket{Code: CodeEchoReply, Identifier: pkt.Identifier, Data: pkt.Data}
			_ = t.SendFrame(protocol, reply.Marshal())
		}
	}
	return nil
}

func runLCP(ctx context.Context, t Transport, cfg LCPConfig) error {
	return negotiatePhase(ctx, t, ProtoLCP,
		cfg.ConfigureRequestOptions,
		func(opts []Option) ([]Option, bool) {
			// Accept the peer's Configure-Request as-is — this client has
			// no MRU/magic-number constraint of its own to enforce on the
			// peer, and Auth-Protocol here just states which method the
			// LNS will challenge us with, handled in runAuth, not here.
			return opts, true
		})
}

func runIPCP(ctx context.Context, t Transport) (*NegotiatedIPCP, error) {
	result := &NegotiatedIPCP{}
	err := negotiatePhase(ctx, t, ProtoIPCP,
		RequestIPCPOptions,
		func(opts []Option) ([]Option, bool) {
			for _, o := range opts {
				result.ApplyOption(o)
			}
			return opts, true
		})
	if err != nil {
		return nil, err
	}
	if result.LocalIP == nil {
		return nil, fmt.Errorf("IPCP completed without an assigned IP address")
	}
	return result, nil
}

func runAuth(ctx context.Context, t Transport, username, password string) error {
	for {
		frameCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		proto, payload, err := t.RecvFrame(frameCtx)
		cancel()
		if err != nil {
			select {
			case <-ctx.Done():
				return fmt.Errorf("timed out waiting for CHAP Challenge")
			default:
				continue
			}
		}
		if proto != ProtoCHAP {
			continue
		}
		pkt, err := ParseCHAPPacket(payload)
		if err != nil {
			return err
		}
		switch pkt.Code {
		case CHAPCodeChallenge:
			resp, err := GenerateMSCHAPv2Response(pkt.Value, username, password)
			if err != nil {
				return err
			}
			reply := CHAPPacket{Code: CHAPCodeResponse, Identifier: pkt.Identifier, Value: resp.Marshal(), Name: []byte(username)}
			if err := t.SendFrame(ProtoCHAP, reply.Marshal()); err != nil {
				return err
			}
		case CHAPCodeSuccess:
			return nil
		case CHAPCodeFailure:
			return fmt.Errorf("CHAP authentication rejected by peer: %s", string(pkt.Message))
		}
	}
}
