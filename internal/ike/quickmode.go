package ike

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"

	"vpn/internal/vpnlog"
)

// IPsec DOI (RFC 2407) protocol identifiers and ESP transform IDs.
const (
	protoIPsecESP = 3

	espDES  = 2
	esp3DES = 3
	espAES  = 12
)

// IPsec SA attribute classes (RFC 2407 §4.5).
const (
	ipsecAttrLifeType        = 1
	ipsecAttrLifeDuration    = 2
	ipsecAttrEncapsulateMode = 4
	ipsecAttrAuthAlgorithm   = 5
	ipsecAttrKeyLength       = 6
)

// Encapsulation Mode values — Tunnel/Transport from RFC 2407, the
// UDP-encapsulated variants from the NAT-T drafts (RFC 3947/3948 lineage).
// This client only ever uses transport-mode ESP (matches entrypoint.sh's
// `type=transport`), and always the UDP-encapsulated variant since this
// client only completes Quick Mode after Phase 1 has already floated to
// port 4500 for NAT-T.
const encapUDPTransport = 4

// Authentication Algorithm values (RFC 2407 §4.5).
const authHMACSHA1 = 2

const payloadNATOA = 21 // RFC 3947 §5.1, same wire shape as an ID payload.

// ChildSA is one direction's worth of established IPsec keying material —
// Quick Mode always produces two of these (inbound and outbound), sharing
// the same cipher/auth choice but each with its own SPI and derived keys.
type ChildSA struct {
	SPI       uint32
	EncKey    []byte
	AuthKey   []byte
	Transform Transform // Encryption/Hash carry the negotiated ESP cipher/HMAC; Group/AuthMethod unused here
}

// QuickModeResult is what a completed Quick Mode exchange hands back to the
// ESP layer.
type QuickModeResult struct {
	Inbound  ChildSA // decrypts packets we receive (our SPI, sent to the peer so *they* use it as ESP's SPI when sending to us)
	Outbound ChildSA // encrypts packets we send (peer's SPI)
}

// espProposalFor mirrors ParseProposal's cipher-hash vocabulary (entrypoint.sh's
// esp= line uses the same "aes256-sha256" style, just without a DH group).
func espProposalFor(s string) (Transform, error) {
	full, err := ParseProposal(s + "-modp1024") // group is irrelevant for ESP (no PFS here); reuse the parser by padding a dummy group
	if err != nil {
		return Transform{}, fmt.Errorf("ESP proposal %q: %w", s, err)
	}
	full.Group = 0
	return full, nil
}

func espTransformID(t Transform) (int, error) {
	switch t.Encryption {
	case Enc3DES:
		return esp3DES, nil
	case EncDES:
		return espDES, nil
	case EncAES:
		return espAES, nil
	default:
		return 0, fmt.Errorf("unsupported ESP encryption algorithm %d", t.Encryption)
	}
}

func marshalESPTransform(number uint8, t Transform, nextPayload uint8) ([]byte, error) {
	txID, err := espTransformID(t)
	if err != nil {
		return nil, err
	}
	var attrs []byte
	attrs = append(attrs, encodeAttrTV(ipsecAttrAuthAlgorithm, authHMACSHA1)...)
	attrs = append(attrs, encodeAttrTV(ipsecAttrEncapsulateMode, encapUDPTransport)...)
	attrs = append(attrs, encodeAttrTV(ipsecAttrLifeType, lifeTypeSeconds)...)
	attrs = append(attrs, encodeAttrTV(ipsecAttrLifeDuration, uint16(t.LifeSecs))...)
	if t.Encryption == EncAES {
		attrs = append(attrs, encodeAttrTV(ipsecAttrKeyLength, uint16(t.KeyBits))...)
	}

	body := make([]byte, 4+len(attrs))
	body[0] = number
	body[1] = byte(txID)
	copy(body[4:], attrs)
	return marshalPayload(nextPayload, body), nil
}

// marshalESPSA builds a Quick Mode SA payload offering one or more ESP
// transforms under a single proposal, our locally generated SPI.
func marshalESPSA(transforms []Transform, spi uint32) ([]byte, error) {
	var txBody []byte
	for i, t := range transforms {
		next := uint8(PayloadTransform)
		if i == len(transforms)-1 {
			next = PayloadNone
		}
		tx, err := marshalESPTransform(uint8(i+1), t, next)
		if err != nil {
			return nil, err
		}
		txBody = append(txBody, tx...)
	}

	spiBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(spiBytes, spi)

	proposal := make([]byte, 4+4+len(txBody))
	proposal[0] = 1             // proposal #
	proposal[1] = protoIPsecESP // protocol-id
	proposal[2] = 4             // SPI size
	proposal[3] = byte(len(transforms))
	copy(proposal[4:8], spiBytes)
	copy(proposal[8:], txBody)

	proposalPayload := marshalPayload(PayloadNone, proposal)

	sa := make([]byte, 8+len(proposalPayload))
	binary.BigEndian.PutUint32(sa[0:4], DOIIPsec)
	binary.BigEndian.PutUint32(sa[4:8], 1) // situation = SIT_IDENTITY_ONLY
	copy(sa[8:], proposalPayload)
	return sa, nil
}

// chosenESP is what parseChosenESPSA extracts from the responder's Quick
// Mode SA payload.
type chosenESP struct {
	SPI       uint32
	Transform Transform
}

func parseChosenESPSA(saBody []byte) (chosenESP, error) {
	if len(saBody) < 8 {
		return chosenESP{}, fmt.Errorf("ESP SA payload too short")
	}
	payloads, err := SplitPayloads(PayloadProposal, saBody[8:])
	if err != nil {
		return chosenESP{}, err
	}
	if len(payloads) != 1 {
		return chosenESP{}, fmt.Errorf("expected exactly one ESP proposal, got %d", len(payloads))
	}
	prop := payloads[0].Body
	if len(prop) < 8 {
		return chosenESP{}, fmt.Errorf("ESP proposal body too short")
	}
	spiSize := int(prop[2])
	numTx := int(prop[3])
	if numTx != 1 {
		return chosenESP{}, fmt.Errorf("expected one ESP transform, got %d", numTx)
	}
	spi := prop[4 : 4+spiSize]
	var spiVal uint32
	for _, b := range spi {
		spiVal = spiVal<<8 | uint32(b)
	}
	txPayloads, err := SplitPayloads(PayloadTransform, prop[4+spiSize:])
	if err != nil || len(txPayloads) != 1 {
		return chosenESP{}, fmt.Errorf("expected one ESP transform payload: %v", err)
	}
	body := txPayloads[0].Body
	if len(body) < 4 {
		return chosenESP{}, fmt.Errorf("ESP transform body too short")
	}
	t := Transform{}
	switch int(body[1]) {
	case esp3DES:
		t.Encryption = Enc3DES
	case espDES:
		t.Encryption = EncDES
	case espAES:
		t.Encryption = EncAES
	default:
		return chosenESP{}, fmt.Errorf("unsupported ESP transform-id %d", body[1])
	}
	data := body[4:]
	for len(data) > 0 {
		if len(data) < 4 {
			break
		}
		typ := binary.BigEndian.Uint16(data[0:2])
		isTV := typ&0x8000 != 0
		attrType := typ &^ 0x8000
		var val uint32
		var consumed int
		if isTV {
			val = uint32(binary.BigEndian.Uint16(data[2:4]))
			consumed = 4
		} else {
			l := int(binary.BigEndian.Uint16(data[2:4]))
			if len(data) < 4+l {
				break
			}
			for _, b := range data[4 : 4+l] {
				val = val<<8 | uint32(b)
			}
			consumed = 4 + l
		}
		switch attrType {
		case ipsecAttrAuthAlgorithm:
			t.Hash = int(val) // reused: HMAC-SHA1==2 happens to coincide with IKE's HashSHA1 value
		case ipsecAttrKeyLength:
			t.KeyBits = int(val)
		}
		data = data[consumed:]
	}
	if t.Encryption == EncAES && t.KeyBits == 0 {
		t.KeyBits = 128
	}
	return chosenESP{SPI: spiVal, Transform: t}, nil
}

func quickModeHash3(hashAlg int, skeyidA []byte, msgID uint32, niB, nrB []byte) ([]byte, error) {
	midBytes := beUint32(msgID)
	data := append([]byte{0}, midBytes...)
	data = append(data, niB...)
	data = append(data, nrB...)
	return prf(hashAlg, skeyidA, data)
}

func beUint32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

// quickModeKeymat derives one direction's ESP keying material, RFC 2409 §5.5
// (no-PFS case): KEYMAT = K1 | K2 | ... where
//
//	K1 = prf(SKEYID_d, protocol | SPI | Ni_b | Nr_b)
//	K2 = prf(SKEYID_d, K1 | protocol | SPI | Ni_b | Nr_b)
//	...
//
// truncated to neededBytes (encryption key length + authentication key
// length for the negotiated ESP transform).
func quickModeKeymat(hashAlg int, skeyidD []byte, protocol uint8, spi uint32, niB, nrB []byte, neededBytes int) ([]byte, error) {
	base := append([]byte{protocol}, beUint32(spi)...)
	base = append(base, niB...)
	base = append(base, nrB...)

	var out []byte
	var prev []byte
	for len(out) < neededBytes {
		input := append(append([]byte{}, prev...), base...)
		k, err := prf(hashAlg, skeyidD, input)
		if err != nil {
			return nil, err
		}
		out = append(out, k...)
		prev = k
	}
	return out[:neededBytes], nil
}

func espKeyLens(t Transform) (encLen, authLen int) {
	switch t.Encryption {
	case Enc3DES:
		encLen = 24
	case EncDES:
		encLen = 8
	case EncAES:
		if t.KeyBits == 0 {
			encLen = 16
		} else {
			encLen = t.KeyBits / 8
		}
	}
	authLen = 20 // HMAC-SHA1 key length (RFC2404) regardless of the 96-bit truncated ICV
	return
}

// EstablishQuickMode negotiates one Phase 2 (IPsec) SA pair over an already
// established Phase 1 session, using entrypoint.sh's transport-mode,
// UDP-encapsulated ESP parameters (leftprotoport/rightprotoport = 17/1701).
func (s *Session) EstablishQuickMode(espProposals []string, localIP, remoteIP net.IP) (*QuickModeResult, error) {
	transforms := make([]Transform, 0, len(espProposals))
	for _, p := range espProposals {
		t, err := espProposalFor(p)
		if err != nil {
			return nil, err
		}
		t.LifeSecs = 3600
		transforms = append(transforms, t)
	}

	var spiBytes [4]byte
	if _, err := rand.Read(spiBytes[:]); err != nil {
		return nil, err
	}
	mySPI := binary.BigEndian.Uint32(spiBytes[:])

	msgID := randomMessageID()
	saBody, err := marshalESPSA(transforms, mySPI)
	if err != nil {
		return nil, err
	}
	ni := make([]byte, 16)
	if _, err := rand.Read(ni); err != nil {
		return nil, err
	}

	bs := blockSize(s.Transform)

	// IDci/IDcr: transport-mode selectors restricted to UDP/1701, matching
	// entrypoint.sh's leftprotoport=17/1701, rightprotoport=17/1701.
	idci := marshalUDPPortID(localIP, 1701)
	idcr := marshalUDPPortID(remoteIP, 1701)

	var natOA1, natOA2 []byte
	if s.floated {
		natOA1 = marshalPayload(payloadNATOA, MarshalIPv4ID(localIP))
		natOA2 = marshalPayload(PayloadNone, MarshalIPv4ID(remoteIP))
	}

	saPayload := marshalPayload(PayloadNonce, saBody)
	noncePayload := marshalPayload(PayloadID, ni)
	idciNext := uint8(PayloadID)
	idcrNext := uint8(PayloadNone)
	if s.floated {
		idcrNext = payloadNATOA
	}
	idciPayload := marshalPayload(idciNext, idci)
	idcrPayload := marshalPayload(idcrNext, idcr)

	rest := append(append([]byte{}, saPayload...), noncePayload...)
	rest = append(rest, idciPayload...)
	rest = append(rest, idcrPayload...)
	rest = append(rest, natOA1...)
	rest = append(rest, natOA2...)

	// RFC 2409 §5.5: "HASH(1) is the prf over the message id (M-ID) from
	// the ISAKMP header concatenated with the entire message that follows
	// the hash [payload], including all payload headers, but excluding any
	// padding added for encryption." So this hashes M-ID followed by the
	// fully-serialized SA/Nonce/IDci/IDcr(/NAT-OA) payloads — headers and
	// all — not just their bodies (unlike Phase 1's HASH_I/HASH_R, which do
	// use bodies only).
	hash1, err := prf(s.Transform.Hash, s.Keys.SKEYIDa, append(beUint32(msgID), rest...))
	if err != nil {
		return nil, err
	}
	hashPayload := marshalPayload(PayloadSA, hash1)

	plain := append(append([]byte{}, hashPayload...), rest...)
	padded := padToBlock(plain, bs)
	// QM1 is the first encrypted message of this new exchange, so its IV is
	// reseeded from the last Phase 1 IV — RFC 2409 §5.5. Every subsequent
	// message of *this same* exchange (QM2, QM3), regardless of which side
	// sends it, chains normally from the previous message's ciphertext tail
	// instead, exactly like MM6 chained from MM5 within Phase 1.
	iv, err := informationalIV(s.Transform.Hash, s.lastIV, msgID, bs)
	if err != nil {
		return nil, err
	}
	cipher, err := cbcEncrypt(s.Transform, s.Keys.EncKey, iv, padded)
	if err != nil {
		return nil, err
	}
	s.lastIV = cipher[len(cipher)-bs:]

	hdr := Header{InitiatorSPI: s.InitiatorSPI, ResponderSPI: s.ResponderSPI, NextPayload: PayloadHash, Version: 0x10, ExchangeType: ExchangeQuickMode, Flags: FlagEncryption, MessageID: msgID}
	hdr.Length = uint32(headerLen + len(cipher))
	qm1 := append(hdr.Marshal(), cipher...)

	vpnlog.Info(stage, "QM1 sent", vpnlog.Fields{"msg_id": msgID, "bytes": len(qm1)})
	resp, err := s.exchangeQuickMode(qm1, msgID)
	if err != nil {
		return nil, fmt.Errorf("QM1/QM2: %w", err)
	}
	respHdr, err := ParseHeader(resp)
	if err != nil {
		return nil, err
	}
	respBody := resp[headerLen:]
	plainResp, err := cbcDecrypt(s.Transform, s.Keys.EncKey, s.lastIV, respBody)
	if err != nil {
		return nil, fmt.Errorf("decrypt QM2: %w", err)
	}
	if len(respBody) >= bs {
		s.lastIV = respBody[len(respBody)-bs:]
	}
	payloads, err := SplitPayloads(respHdr.NextPayload, plainResp)
	if err != nil {
		return nil, fmt.Errorf("QM2 payload chain: %w", err)
	}
	var peerSABody, peerNonce []byte
	for _, p := range payloads {
		switch p.Type {
		case PayloadSA:
			peerSABody = p.Body
		case PayloadNonce:
			peerNonce = p.Body
		}
	}
	if peerSABody == nil || peerNonce == nil {
		return nil, fmt.Errorf("IPSEC_FAILURE: QM2 missing SA or Nonce")
	}
	chosen, err := parseChosenESPSA(peerSABody)
	if err != nil {
		return nil, fmt.Errorf("IPSEC_FAILURE: %w", err)
	}

	// QM3: HDR*, HASH(3) — acknowledges completion, RFC 2409 §5.5.
	hash3, err := quickModeHash3(s.Transform.Hash, s.Keys.SKEYIDa, msgID, ni, peerNonce)
	if err != nil {
		return nil, err
	}
	hash3Payload := marshalPayload(PayloadNone, hash3)
	padded3 := padToBlock(hash3Payload, bs)
	cipher3, err := cbcEncrypt(s.Transform, s.Keys.EncKey, s.lastIV, padded3)
	if err != nil {
		return nil, err
	}
	s.lastIV = cipher3[len(cipher3)-bs:]
	hdr3 := Header{InitiatorSPI: s.InitiatorSPI, ResponderSPI: s.ResponderSPI, NextPayload: PayloadHash, Version: 0x10, ExchangeType: ExchangeQuickMode, Flags: FlagEncryption, MessageID: msgID}
	hdr3.Length = uint32(headerLen + len(cipher3))
	qm3 := append(hdr3.Marshal(), cipher3...)
	if err := s.sendRaw(qm3); err != nil {
		return nil, fmt.Errorf("send QM3: %w", err)
	}
	vpnlog.Info(stage, "QM3 sent (exchange complete)", nil)

	encLen, authLen := espKeyLens(chosen.Transform)
	outKeymat, err := quickModeKeymat(s.Transform.Hash, s.Keys.SKEYIDd, protoIPsecESP, chosen.SPI, ni, peerNonce, encLen+authLen)
	if err != nil {
		return nil, err
	}
	inKeymat, err := quickModeKeymat(s.Transform.Hash, s.Keys.SKEYIDd, protoIPsecESP, mySPI, ni, peerNonce, encLen+authLen)
	if err != nil {
		return nil, err
	}

	result := &QuickModeResult{
		Outbound: ChildSA{SPI: chosen.SPI, EncKey: outKeymat[:encLen], AuthKey: outKeymat[encLen:], Transform: chosen.Transform},
		Inbound:  ChildSA{SPI: mySPI, EncKey: inKeymat[:encLen], AuthKey: inKeymat[encLen:], Transform: chosen.Transform},
	}
	vpnlog.Info(stage, "Quick Mode ESTABLISHED", vpnlog.Fields{"in_spi": mySPI, "out_spi": chosen.SPI})
	return result, nil
}

func marshalUDPPortID(ip net.IP, port uint16) []byte {
	body := make([]byte, 8)
	body[0] = IDTypeIPv4Addr
	body[1] = 17 // UDP
	binary.BigEndian.PutUint16(body[2:4], port)
	copy(body[4:8], ip.To4())
	return body
}

func randomMessageID() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	v := binary.BigEndian.Uint32(b[:])
	if v == 0 {
		v = 1 // Message-ID 0 is reserved for Phase 1
	}
	return v
}
