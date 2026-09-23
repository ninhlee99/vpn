package ike

import (
	"fmt"
	"net"
)

// ID types, RFC 2407 §4.6.2.1. Matches entrypoint.sh's `left=%defaultroute`:
// strongSwan identifies the client by its own outbound IPv4 address, so this
// client does the same rather than inventing an FQDN/KeyID identity the
// server was never configured to expect.
const (
	IDTypeIPv4Addr = 1
	IDTypeFQDN     = 2
	IDTypeUserFQDN = 3
	IDTypeKeyID    = 11
)

// MarshalID encodes an ID payload body (RFC 2408 §3.6): ID type, DOI-specific
// protocol/port (both 0 = unspecified, standard for this ID type), then the
// identification data itself.
func MarshalID(idType uint8, data []byte) []byte {
	body := make([]byte, 4+len(data))
	body[0] = idType
	// bytes 1: protocol id, 0 = unspecified
	// bytes 2-3: port, 0 = unspecified
	copy(body[4:], data)
	return body
}

// MarshalIPv4ID builds an ID_IPV4_ADDR identity payload body from a local
// address.
func MarshalIPv4ID(ip net.IP) []byte {
	return MarshalID(IDTypeIPv4Addr, ip.To4())
}

// ParsedID is a decoded ID payload.
type ParsedID struct {
	Type uint8
	Data []byte
}

func ParseID(body []byte) (ParsedID, error) {
	if len(body) < 4 {
		return ParsedID{}, errShort("ID payload")
	}
	return ParsedID{Type: body[0], Data: append([]byte{}, body[4:]...)}, nil
}

// String renders the identity the way a user writes it in server_id: dotted
// IPv4 for ID_IPV4_ADDR, the raw text for FQDN/USER_FQDN/KEY_ID. Any other
// type gets a form no plausible server_id can match, so an unexpected
// identity type fails the comparison instead of skipping it.
func (id ParsedID) String() string {
	switch id.Type {
	case IDTypeIPv4Addr:
		if len(id.Data) == net.IPv4len {
			return net.IP(id.Data).String()
		}
	case IDTypeFQDN, IDTypeUserFQDN, IDTypeKeyID:
		return string(id.Data)
	}
	return fmt.Sprintf("<ID type %d, %d bytes>", id.Type, len(id.Data))
}

func errShort(what string) error {
	return &shortPayloadError{what}
}

type shortPayloadError struct{ what string }

func (e *shortPayloadError) Error() string { return e.what + " is too short" }
