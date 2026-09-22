// Package l2tp implements the L2TPv2 (RFC 2661) control-connection and
// data-channel framing needed to establish one tunnel and one session
// against an L2TP/IPsec LNS, matching the reference config's
// `[lac myvpn]` xl2tpd setup (~/l2tp-proxy/entrypoint.sh).
package l2tp

import (
	"encoding/binary"
	"fmt"
)

// Header flag bits, RFC 2661 §3.1.
const (
	flagType     = 1 << 15 // T: 1 = control message, 0 = data message
	flagLength   = 1 << 14 // L: Length field present
	flagSequence = 1 << 11 // S: Ns/Nr fields present
	flagOffset   = 1 << 9  // O: Offset Size field present
	flagPriority = 1 << 8  // P: priority (data messages only)
	versionMask  = 0x000F
	protocolVer2 = 2
)

// Header is the common L2TP header. Control messages always set
// Type+Length+Sequence; data messages in this client never set Offset
// (no need to skip payload padding we didn't add) and never set Priority.
type Header struct {
	IsControl bool
	TunnelID  uint16
	SessionID uint16
	Ns, Nr    uint16 // only meaningful when IsControl (data messages here don't use sequencing)
	HasSeq    bool
}

// MarshalControl encodes a control message: header + AVP bytes, with the
// Length field computed and filled in (RFC 2661 §3.1 requires L=1, S=1 for
// control messages).
func MarshalControl(tunnelID, sessionID, ns, nr uint16, avps []byte) []byte {
	flags := uint16(flagType | flagLength | flagSequence | protocolVer2)
	body := make([]byte, 12+len(avps))
	binary.BigEndian.PutUint16(body[0:2], flags)
	// body[2:4] (Length) filled below once total size is known.
	binary.BigEndian.PutUint16(body[4:6], tunnelID)
	binary.BigEndian.PutUint16(body[6:8], sessionID)
	binary.BigEndian.PutUint16(body[8:10], ns)
	binary.BigEndian.PutUint16(body[10:12], nr)
	copy(body[12:], avps)
	binary.BigEndian.PutUint16(body[2:4], uint16(len(body)))
	return body
}

// MarshalData encodes a data message carrying one PPP frame. No sequencing,
// no offset — the simplest legal data header, which is all the reference
// LNS requires (xl2tpd's default data path does not mandate sequenced data).
func MarshalData(tunnelID, sessionID uint16, pppFrame []byte) []byte {
	flags := uint16(protocolVer2) // T=0 (data), L=0, S=0, O=0
	body := make([]byte, 6+len(pppFrame))
	binary.BigEndian.PutUint16(body[0:2], flags)
	binary.BigEndian.PutUint16(body[2:4], tunnelID)
	binary.BigEndian.PutUint16(body[4:6], sessionID)
	copy(body[6:], pppFrame)
	return body
}

// ParsedMessage is a decoded L2TP message (control or data).
type ParsedMessage struct {
	Header  Header
	AVPs    []AVP  // populated for control messages
	Payload []byte // populated for data messages (raw PPP frame)
}

// Parse decodes any L2TP message, following the flag bits to know which
// optional fields are present rather than assuming a fixed layout — the
// header is variable-length by design (RFC 2661 §3.1).
func Parse(b []byte) (*ParsedMessage, error) {
	if len(b) < 6 {
		return nil, fmt.Errorf("L2TP message too short: %d bytes", len(b))
	}
	flags := binary.BigEndian.Uint16(b[0:2])
	offset := 2
	isControl := flags&flagType != 0

	if flags&flagLength != 0 {
		if len(b) < offset+2 {
			return nil, fmt.Errorf("truncated Length field")
		}
		offset += 2
	}
	if len(b) < offset+4 {
		return nil, fmt.Errorf("truncated Tunnel/Session ID")
	}
	tunnelID := binary.BigEndian.Uint16(b[offset : offset+2])
	sessionID := binary.BigEndian.Uint16(b[offset+2 : offset+4])
	offset += 4

	h := Header{IsControl: isControl, TunnelID: tunnelID, SessionID: sessionID}

	if flags&flagSequence != 0 {
		if len(b) < offset+4 {
			return nil, fmt.Errorf("truncated Ns/Nr fields")
		}
		h.Ns = binary.BigEndian.Uint16(b[offset : offset+2])
		h.Nr = binary.BigEndian.Uint16(b[offset+2 : offset+4])
		h.HasSeq = true
		offset += 4
	}
	if flags&flagOffset != 0 {
		if len(b) < offset+2 {
			return nil, fmt.Errorf("truncated Offset Size field")
		}
		offsetSize := int(binary.BigEndian.Uint16(b[offset : offset+2]))
		offset += 2 + offsetSize
	}
	if offset > len(b) {
		return nil, fmt.Errorf("header fields exceed message length")
	}

	if !isControl {
		return &ParsedMessage{Header: h, Payload: b[offset:]}, nil
	}

	avps, err := ParseAVPs(b[offset:])
	if err != nil {
		return nil, fmt.Errorf("parse AVPs: %w", err)
	}
	return &ParsedMessage{Header: h, AVPs: avps}, nil
}
