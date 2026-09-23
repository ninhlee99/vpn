package ike

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"fmt"
	"net"
	"time"

	"vpn/internal/vpnlog"
)

const stage = "IKE"

// retransmit tuning: an unanswered message is resent up to maxRetransmits
// times with a fixed backoff before the exchange gives up with a timeout —
// simple and bounded, matching how racoon/strongSwan behave by default
// (their defaults are a few seconds per retry, a handful of retries).
const (
	retransmitInterval = 3 * time.Second
	maxRetransmits     = 5
)

// Session is an established Phase 1 (IKE) SA: everything Phase 2 (Quick
// Mode / ESP) needs to proceed, without re-deriving anything.
//
// The socket is deliberately *unconnected* (net.ListenUDP, not DialUDP).
// A connected UDP socket makes the kernel silently drop any datagram whose
// source port doesn't match the address it was "connected" to — which is
// exactly wrong here: a real server (confirmed live against the reference
// VPN server) keeps replying to Informational/Notify exchanges from port
// 500 even after Main Mode has floated the client to port 4500 for NAT-T.
// An unconnected socket lets this client accept any port from the server's
// IP, the way every real IKE daemon does, instead of missing half the
// conversation.
type Session struct {
	conn         *net.UDPConn
	serverIP     net.IP
	destAddr     *net.UDPAddr // where the next message should be sent — :500 until floated, then :4500
	InitiatorSPI [8]byte
	ResponderSPI [8]byte
	Keys         *Phase1Keys
	Transform    Transform
	NATDetected  bool
	LocalIP      net.IP
	nextMsgID    uint32
	lastIV       []byte // last ciphertext block sent/received in this phase, seeds the next message's IV
	floated      bool   // once true, every send/receive is framed with RFC 3947/3948's 4-byte non-ESP marker
}

// nonESPMarker is RFC 3947 §3's 4 zero bytes prepended to every IKE (not
// ESP) message once negotiation has floated to UDP/4500 — required so the
// receiver can tell an IKE control message apart from a UDP-encapsulated
// ESP packet arriving on the same port.
var nonESPMarker = []byte{0, 0, 0, 0}

// Config is everything needed to run Phase 1 against one server.
type Config struct {
	ServerHost string
	ServerID   string // expected IDr; empty means accept any (matches entrypoint.sh's rightid=%any)
	PSK        string
	Proposals  []string // e.g. entrypoint.sh's ike= list, most-preferred first
	LocalIP    net.IP   // our outbound address, used as our ID_IPV4_ADDR (matches left=%defaultroute)
}

// EstablishPhase1 runs IKEv1 Main Mode with PSK authentication end-to-end
// (MM1–MM6) against cfg.ServerHost:500, including RFC 3947 NAT-T detection
// and, if NAT is found, floating to :4500 for MM5/MM6 as required.
func EstablishPhase1(ctx context.Context, cfg Config) (*Session, error) {
	transforms := make([]Transform, 0, len(cfg.Proposals))
	for _, p := range cfg.Proposals {
		t, err := ParseProposal(p)
		if err != nil {
			return nil, fmt.Errorf("configured IKE proposal: %w", err)
		}
		transforms = append(transforms, t)
	}

	serverAddr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(cfg.ServerHost, "500"))
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", cfg.ServerHost, err)
	}
	// Bind our own source port to 500 for the pre-NAT-T exchange: some IKE responders key their
	// NAT-D "my view of your address" computation, and some firewalls'
	// UDP/500 conntrack entries, on the client also using port 500 for the
	// pre-NAT-T exchange — matching what every real IKE client does.
	//
	// Bind to cfg.LocalIP specifically, not the wildcard address: NAT-T
	// replaces this UDP/500 socket with one bound to UDP/4500 after MM4.
	// L2TP's "port 1701" is a virtual header this client builds
	// inside the ESP payload in internal/engine/transport.go, never a real
	// socket, so there's nothing to bind there). A wildcard bind lets the
	// kernel repick the source address/interface for every sendto against
	// whatever the routing table says *at that moment* — exactly what
	// bit us once ApplyFullTunnel's split-default routes are in the
	// picture and something reshuffles the route table. Pinning to a
	// specific local address removes that ambiguity.
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: cfg.LocalIP, Port: 500})
	if err != nil {
		return nil, fmt.Errorf("bind local UDP/500 on %s (needs root, or another IKE client is already using it): %w", cfg.LocalIP, err)
	}

	sess := &Session{conn: conn, serverIP: serverAddr.IP, destAddr: serverAddr, LocalIP: cfg.LocalIP}
	if _, err := rand.Read(sess.InitiatorSPI[:]); err != nil {
		conn.Close()
		return nil, fmt.Errorf("generate initiator SPI: %w", err)
	}

	if err := sess.runMainMode(ctx, cfg, transforms); err != nil {
		conn.Close()
		return nil, err
	}
	return sess, nil
}

// exchange sends msg to s.destAddr and waits for a reply from the server's
// IP on *any* source port (see the Session doc comment for why). It keeps
// reading (without re-sending) as long as replies keep arriving but don't
// look like the expected next message — an encrypted Informational/Notify
// interleaved with the real reply is normal IKE traffic, not a failure —
// only a full retransmit-budget of silence is IKE_TIMEOUT.
func (s *Session) exchange(ctx context.Context, msg []byte, expectMinLen int) ([]byte, error) {
	wire := msg
	if s.floated {
		wire = append(append([]byte{}, nonESPMarker...), msg...)
	}
	var lastErr error
	for attempt := 0; attempt <= maxRetransmits; attempt++ {
		if attempt > 0 {
			vpnlog.Debug(stage, "retransmitting", vpnlog.Fields{"attempt": attempt})
		}
		if _, err := s.conn.WriteToUDP(wire, s.destAddr); err != nil {
			return nil, fmt.Errorf("send: %w", err)
		}
		deadline := time.Now().Add(retransmitInterval)
		for {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				lastErr = fmt.Errorf("no reply within %s", retransmitInterval)
				break
			}
			s.conn.SetReadDeadline(deadline)
			buf := make([]byte, 65535)
			n, from, err := s.conn.ReadFromUDP(buf)
			if err != nil {
				lastErr = err
				break
			}
			if !from.IP.Equal(s.serverIP) {
				continue // stray packet from something else on this port
			}
			got := buf[:n]
			floatedMsg := from.Port == 4500
			if floatedMsg {
				if n < 4 || !bytes.Equal(got[:4], nonESPMarker) {
					vpnlog.Debug(stage, "dropped :4500 packet missing non-ESP marker", vpnlog.Fields{"bytes": n})
					continue
				}
				got = got[4:]
			}
			if len(got) < headerLen {
				continue
			}
			h, err := ParseHeader(got)
			if err == nil && h.ExchangeType == ExchangeInformational {
				vpnlog.Info(stage, "informational SPIs", vpnlog.Fields{
					"got_i": fmt.Sprintf("%x", h.InitiatorSPI), "got_r": fmt.Sprintf("%x", h.ResponderSPI),
					"our_i": fmt.Sprintf("%x", s.InitiatorSPI), "our_r": fmt.Sprintf("%x", s.ResponderSPI),
				})
				s.logInformational(h, got[headerLen:])
				continue // not the message this call is waiting for — keep listening
			}
			if len(got) < expectMinLen {
				lastErr = fmt.Errorf("short response: %d bytes", len(got))
				continue
			}
			return got, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
	return nil, fmt.Errorf("IKE_TIMEOUT: no response after %d attempts: %v", maxRetransmits+1, lastErr)
}

// SendESP sends one already-framed ESP packet (SPI|Seq|IV|ciphertext|ICV,
// see internal/ipsec) over this session's socket. ESP packets get no
// non-ESP marker — RFC 3948 §2.1: the receiver tells IKE and ESP traffic on
// the same port 4500 apart by checking whether the first 4 bytes are all
// zero (IKE) or not (ESP; a real SPI is never zero).
func (s *Session) SendESP(pkt []byte) error {
	_, err := s.conn.WriteToUDP(pkt, s.destAddr)
	return err
}

// RecvESP blocks for the next ESP packet from the server, transparently
// consuming (and logging) any interleaved IKE Informational message instead
// of returning it — mirrors exchangeQuickMode's tolerance for keepalives.
func (s *Session) RecvESP(ctx context.Context) ([]byte, error) {
	for {
		deadline, ok := ctx.Deadline()
		if !ok {
			deadline = time.Now().Add(30 * time.Second)
		}
		if err := s.conn.SetReadDeadline(deadline); err != nil {
			return nil, err
		}
		buf := make([]byte, 65535)
		n, from, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return nil, err
		}
		if !from.IP.Equal(s.serverIP) {
			continue
		}
		got := buf[:n]
		if n >= 4 && bytes.Equal(got[:4], nonESPMarker) {
			// An IKE control message (Informational/DPD) arrived
			// interleaved with ESP traffic — handle and keep waiting.
			if n >= 4+headerLen {
				if h, err := ParseHeader(got[4:]); err == nil {
					s.logInformational(h, got[4+headerLen:])
				}
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
			continue
		}
		return got, nil
	}
}

// sendRaw writes one already-framed message (marker added if floated) to
// the current destAddr, no retransmit/response handling — used for QM3,
// which per RFC 2409 §5.5 is a one-shot acknowledgement the initiator does
// not wait for a reply to.
func (s *Session) sendRaw(msg []byte) error {
	wire := msg
	if s.floated {
		wire = append(append([]byte{}, nonESPMarker...), msg...)
	}
	_, err := s.conn.WriteToUDP(wire, s.destAddr)
	return err
}

// exchangeQuickMode retransmits msg until a reply carrying the same
// Message-ID and ExchangeType==QuickMode arrives, the same
// keep-listening-past-Informational behavior as exchange().
func (s *Session) exchangeQuickMode(msg []byte, msgID uint32) ([]byte, error) {
	wire := msg
	if s.floated {
		wire = append(append([]byte{}, nonESPMarker...), msg...)
	}
	var lastErr error
	for attempt := 0; attempt <= maxRetransmits; attempt++ {
		if attempt > 0 {
			vpnlog.Debug(stage, "QM retransmitting", vpnlog.Fields{"attempt": attempt})
		}
		if _, err := s.conn.WriteToUDP(wire, s.destAddr); err != nil {
			return nil, fmt.Errorf("send: %w", err)
		}
		deadline := time.Now().Add(retransmitInterval)
		for {
			if time.Until(deadline) <= 0 {
				lastErr = fmt.Errorf("no reply within %s", retransmitInterval)
				break
			}
			s.conn.SetReadDeadline(deadline)
			buf := make([]byte, 65535)
			n, from, err := s.conn.ReadFromUDP(buf)
			if err != nil {
				lastErr = err
				break
			}
			if !from.IP.Equal(s.serverIP) {
				continue
			}
			got := buf[:n]
			if from.Port == 4500 {
				if n < 4 || !bytes.Equal(got[:4], nonESPMarker) {
					continue
				}
				got = got[4:]
			}
			if len(got) < headerLen {
				continue
			}
			h, err := ParseHeader(got)
			if err != nil {
				continue
			}
			if h.ExchangeType == ExchangeInformational {
				s.logInformational(h, got[headerLen:])
				continue
			}
			if h.MessageID != msgID {
				continue // reply to a different exchange — keep waiting
			}
			return got, nil
		}
	}
	return nil, fmt.Errorf("IKE_TIMEOUT: Quick Mode: no response after %d attempts: %v", maxRetransmits+1, lastErr)
}

// logInformational best-effort decrypts a Notify/Informational exchange
// using whatever Phase 1 keys have been derived so far, purely for
// diagnostics — the server sends these for real protocol reasons (rejecting
// a message, DPD, SA deletion) and silently dropping them without at least
// logging the notify type makes failures much harder to diagnose.
func (s *Session) logInformational(h Header, encBody []byte) {
	if s.Keys == nil || len(encBody) == 0 {
		vpnlog.Info(stage, "received Informational exchange (no keys yet to decrypt)", nil)
		return
	}
	bs := blockSize(s.Transform)
	if len(encBody)%bs != 0 || s.lastIV == nil {
		vpnlog.Info(stage, "received Informational exchange (undecryptable)", nil)
		return
	}
	// RFC 2409 §5.5: a new exchange (Informational or Quick Mode) does not
	// reuse the last Phase 1 ciphertext block as its IV directly — it seeds
	// a fresh one from hash(last Phase 1 IV | this message's Message-ID).
	iv, err := informationalIV(s.Transform.Hash, s.lastIV, h.MessageID, bs)
	if err != nil {
		vpnlog.Info(stage, "received Informational exchange (IV derivation failed)", vpnlog.Fields{"err": err})
		return
	}
	plain, err := cbcDecrypt(s.Transform, s.Keys.EncKey, iv, encBody)
	if err != nil {
		vpnlog.Info(stage, "received Informational exchange (decrypt failed)", vpnlog.Fields{"err": err})
		return
	}
	payloads, err := SplitPayloads(h.NextPayload, plain)
	if err != nil {
		vpnlog.Info(stage, "received Informational exchange (undecodable after decrypt)", vpnlog.Fields{
			"next_payload": h.NextPayload, "plain_len": len(plain), "parse_err": err,
		})
		return
	}
	for _, p := range payloads {
		if p.Type == PayloadNotify && len(p.Body) >= 8 {
			notifyType := uint16(p.Body[6])<<8 | uint16(p.Body[7])
			vpnlog.Info(stage, "server sent Notify", vpnlog.Fields{"notify_type": notifyType})
		}
	}
}

func (s *Session) runMainMode(ctx context.Context, cfg Config, transforms []Transform) error {
	start := time.Now()

	// --- MM1: HDR, SA[, VID(NAT-T)] ---
	sa := MarshalSA(transforms)
	vid := marshalPayload(PayloadNone, RFC3947VendorID())
	saPayload := marshalPayload(PayloadVendorID, sa)
	body := append(saPayload, vid...)

	hdr1 := Header{InitiatorSPI: s.InitiatorSPI, NextPayload: PayloadSA, Version: 0x10, ExchangeType: ExchangeIdentityProt}
	hdr1.Length = uint32(headerLen + len(body))
	mm1 := append(hdr1.Marshal(), body...)

	vpnlog.Info(stage, "MM1 sent (SA proposal)", vpnlog.Fields{"proposals": cfg.Proposals})
	resp, err := s.exchange(ctx, mm1, headerLen)
	if err != nil {
		return fmt.Errorf("MM1/MM2: %w", err)
	}
	hdr2, err := ParseHeader(resp)
	if err != nil {
		return err
	}
	s.ResponderSPI = hdr2.ResponderSPI
	payloads2, err := SplitPayloads(hdr2.NextPayload, resp[headerLen:])
	if err != nil {
		return fmt.Errorf("parse MM2: %w", err)
	}
	var chosenSABody []byte
	peerSupportsNATT := false
	for _, p := range payloads2 {
		switch p.Type {
		case PayloadSA:
			chosenSABody = p.Body
		case PayloadVendorID:
			if string(p.Body) == string(RFC3947VendorID()) {
				peerSupportsNATT = true
			}
		case PayloadNotify:
			return fmt.Errorf("IKE_PROPOSAL_MISMATCH: server sent Notify instead of SA in MM2 (likely NO_PROPOSAL_CHOSEN)")
		}
	}
	if chosenSABody == nil {
		return fmt.Errorf("IKE_PROPOSAL_MISMATCH: MM2 did not contain an SA payload")
	}
	chosen, err := ParseChosenTransform(chosenSABody)
	if err != nil {
		return fmt.Errorf("IKE_PROPOSAL_MISMATCH: %w", err)
	}
	s.Transform = chosen
	vpnlog.Info(stage, "MM2 received (server chose transform)", vpnlog.Fields{
		"encryption": chosen.Encryption, "hash": chosen.Hash, "group": chosen.Group, "nat_t_vendor": peerSupportsNATT,
	})

	// --- MM3: HDR, KE, Nonce[, NAT-D, NAT-D] ---
	group, ok := Groups[chosen.Group]
	if !ok {
		return fmt.Errorf("server chose unsupported DH group %d", chosen.Group)
	}
	kp, err := GenerateKeyPair(group)
	if err != nil {
		return err
	}
	ni := make([]byte, 32)
	if _, err := rand.Read(ni); err != nil {
		return err
	}

	kePayload := marshalPayload(PayloadNonce, kp.PublicBytes())
	var nonceNext uint8 = PayloadNone
	var natdBody []byte
	if peerSupportsNATT {
		nonceNext = payloadNATD
	}
	noncePayload := marshalPayload(nonceNext, ni)
	body3 := append(kePayload, noncePayload...)

	if peerSupportsNATT {
		localPort := uint16(500)
		natdLocal, err := computeNATD(chosen.Hash, s.InitiatorSPI, s.ResponderSPI, cfg.LocalIP, localPort)
		if err != nil {
			return err
		}
		natdRemote, err := computeNATD(chosen.Hash, s.InitiatorSPI, s.ResponderSPI, s.serverIP, 500)
		if err != nil {
			return err
		}
		natdLocalPayload := marshalPayload(payloadNATD, natdLocal)
		natdRemotePayload := marshalPayload(PayloadNone, natdRemote)
		natdBody = append(natdLocalPayload, natdRemotePayload...)
	}
	body3 = append(body3, natdBody...)

	hdr3 := Header{InitiatorSPI: s.InitiatorSPI, ResponderSPI: s.ResponderSPI, NextPayload: PayloadKE, Version: 0x10, ExchangeType: ExchangeIdentityProt}
	hdr3.Length = uint32(headerLen + len(body3))
	mm3 := append(hdr3.Marshal(), body3...)

	vpnlog.Info(stage, "MM3 sent (KE, nonce)", vpnlog.Fields{"group": chosen.Group})
	resp4, err := s.exchange(ctx, mm3, headerLen)
	if err != nil {
		return fmt.Errorf("MM3/MM4: %w", err)
	}
	hdr4, err := ParseHeader(resp4)
	if err != nil {
		return err
	}
	payloads4, err := SplitPayloads(hdr4.NextPayload, resp4[headerLen:])
	if err != nil {
		return fmt.Errorf("parse MM4: %w", err)
	}
	var peerKE, peerNonce []byte
	var peerNATDs [][]byte
	for _, p := range payloads4 {
		switch p.Type {
		case PayloadKE:
			peerKE = p.Body
		case PayloadNonce:
			peerNonce = p.Body
		case payloadNATD:
			peerNATDs = append(peerNATDs, p.Body)
		}
	}
	if peerKE == nil || peerNonce == nil {
		return fmt.Errorf("IKE_AUTH_FAILED: MM4 missing KE or Nonce payload")
	}

	if peerSupportsNATT && len(peerNATDs) == 2 {
		expectMine, _ := computeNATD(chosen.Hash, s.InitiatorSPI, s.ResponderSPI, cfg.LocalIP, 500)
		matchMine := bytes.Equal(expectMine, peerNATDs[1])
		expectServer, _ := computeNATD(chosen.Hash, s.InitiatorSPI, s.ResponderSPI, s.serverIP, 500)
		matchServer := bytes.Equal(expectServer, peerNATDs[0])
		s.NATDetected = !matchMine || !matchServer
	}
	vpnlog.Info(stage, "MM4 received", vpnlog.Fields{"nat_detected": s.NATDetected})

	gxy := kp.SharedSecret(peerKE)
	keys, err := DerivePhase1Keys(chosen, []byte(cfg.PSK), ni, peerNonce, gxy, s.InitiatorSPI, s.ResponderSPI)
	if err != nil {
		return fmt.Errorf("derive Phase 1 keys: %w", err)
	}
	s.Keys = keys

	if s.NATDetected {
		// RFC 3947 §4 requires both ports to change to 4500 before MM5.
		// Keeping the local socket on :500 works through permissive NATs,
		// but IPsec-aware NATs can special-case that port and drop NAT-T ESP.
		if err := s.floatToNATT(cfg.LocalIP); err != nil {
			return err
		}
	}

	// IV0 = hash(g^xi | g^xr), truncated to block size — RFC 2409 §5.
	ivSeed, err := digest(chosen.Hash, append(append([]byte{}, kp.PublicBytes()...), peerKE...))
	if err != nil {
		return err
	}
	bs := blockSize(chosen)
	s.lastIV = ivSeed[:bs]

	// --- MM5*: HDR*, IDii, HASH_I (encrypted) ---
	idBody := MarshalIPv4ID(cfg.LocalIP)
	saBodyForHash := sa // SAi_b is the Phase 1 SA payload body sent in MM1 (without generic header)

	hashI, err := keys.ComputeHashI(kp.PublicBytes(), peerKE, s.InitiatorSPI, s.ResponderSPI, saBodyForHash, idBody)
	if err != nil {
		return err
	}

	idPayload := marshalPayload(PayloadHash, idBody)
	hashPayload := marshalPayload(PayloadNone, hashI)
	plain5 := append(idPayload, hashPayload...)
	padded5 := padToBlock(plain5, bs)
	cipher5, err := cbcEncrypt(chosen, keys.EncKey, s.lastIV, padded5)
	if err != nil {
		return err
	}
	s.lastIV = cipher5[len(cipher5)-bs:]

	hdr5 := Header{InitiatorSPI: s.InitiatorSPI, ResponderSPI: s.ResponderSPI, NextPayload: PayloadID, Version: 0x10, ExchangeType: ExchangeIdentityProt, Flags: FlagEncryption}
	hdr5.Length = uint32(headerLen + len(cipher5))
	mm5 := append(hdr5.Marshal(), cipher5...)

	vpnlog.Info(stage, "MM5 sent (encrypted identity + auth)", nil)
	resp6, err := s.exchange(ctx, mm5, headerLen)
	if err != nil {
		return fmt.Errorf("MM5/MM6: %w", err)
	}
	hdr6, err := ParseHeader(resp6)
	if err != nil {
		return err
	}
	encBody6 := resp6[headerLen:]
	ivIn := s.lastIV
	plain6, err := cbcDecrypt(chosen, keys.EncKey, ivIn, encBody6)
	if err != nil {
		return fmt.Errorf("decrypt MM6: %w", err)
	}
	if len(encBody6) >= bs {
		s.lastIV = encBody6[len(encBody6)-bs:]
	}
	payloads6, err := SplitPayloads(hdr6.NextPayload, plain6)
	if err != nil {
		return fmt.Errorf("IKE_AUTH_FAILED: parse MM6 (likely wrong PSK): %w", err)
	}
	var peerIDBody, peerHash []byte
	for _, p := range payloads6 {
		switch p.Type {
		case PayloadID:
			peerIDBody = p.Body
		case PayloadHash:
			peerHash = p.Body
		}
	}
	if peerIDBody == nil || peerHash == nil {
		return fmt.Errorf("IKE_AUTH_FAILED: MM6 missing ID or HASH payload (wrong PSK, or server rejected our identity)")
	}
	expectHashR, err := keys.ComputeHashR(peerKE, kp.PublicBytes(), s.ResponderSPI, s.InitiatorSPI, saBodyForHash, peerIDBody)
	if err != nil {
		return err
	}
	if !hmac.Equal(expectHashR, peerHash) {
		return fmt.Errorf("IKE_AUTH_FAILED: HASH_R mismatch (PSK likely incorrect)")
	}
	if err := checkServerID(cfg.ServerID, peerIDBody); err != nil {
		return err
	}

	s.nextMsgID = 0
	vpnlog.Timed(stage, "Phase 1 ESTABLISHED", start)
	return nil
}

// floatToNATT replaces the pre-negotiation UDP/500 socket with UDP/4500.
// Bind first so failure leaves the established Phase 1 socket intact.
func (s *Session) floatToNATT(localIP net.IP) error {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: localIP, Port: 4500})
	if err != nil {
		return fmt.Errorf("bind local UDP/4500 for NAT-T on %s: %w", localIP, err)
	}
	oldConn := s.conn
	s.conn = conn
	s.destAddr = &net.UDPAddr{IP: s.serverIP, Port: 4500}
	s.floated = true
	if err := oldConn.Close(); err != nil {
		conn.Close()
		s.conn = oldConn
		s.destAddr = &net.UDPAddr{IP: s.serverIP, Port: 500}
		s.floated = false
		return fmt.Errorf("close pre-NAT-T UDP/500 socket: %w", err)
	}
	vpnlog.Info(stage, "floated to UDP/4500 for NAT-T", vpnlog.Fields{"local_port": 4500})
	return nil
}

// informationalIV computes the IV a new exchange (Informational or Quick
// Mode) uses for its first encrypted message, RFC 2409 §5.5:
// IV = hash(last-Phase-1-IV | Message-ID), truncated to the cipher's block
// size. The Message-ID is the 4-byte big-endian value from that exchange's
// own ISAKMP header.
func informationalIV(hashAlg int, lastPhase1IV []byte, messageID uint32, blockLen int) ([]byte, error) {
	msgIDBytes := []byte{byte(messageID >> 24), byte(messageID >> 16), byte(messageID >> 8), byte(messageID)}
	seed, err := digest(hashAlg, append(append([]byte{}, lastPhase1IV...), msgIDBytes...))
	if err != nil {
		return nil, err
	}
	return seed[:blockLen], nil
}

// checkServerID enforces the profile's server_id against the responder's
// (HASH_R-authenticated) ID payload. Empty want accepts any identity. Every
// ID type is compared — an identity type this client doesn't recognize, or
// an unparsable payload, is a mismatch rather than a silent pass.
func checkServerID(want string, peerIDBody []byte) error {
	if want == "" {
		return nil
	}
	pid, err := ParseID(peerIDBody)
	if err != nil {
		return fmt.Errorf("IKE_AUTH_FAILED: parse server ID payload: %w", err)
	}
	if got := pid.String(); got != want {
		return fmt.Errorf("IKE_AUTH_FAILED: server identified itself as %s, expected %s (set server_id to match, or leave empty to accept any)", got, want)
	}
	return nil
}

// payloadNATD is RFC 3947 §5's NAT-D payload type, registered as ISAKMP
// payload type 20. It postdates the original RFC 2408 payload registry, so
// it is not one of the PayloadXxx constants in isakmp.go.
const payloadNATD = 20
