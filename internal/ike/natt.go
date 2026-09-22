package ike

import (
	"crypto/md5" //nolint:gosec // required: RFC 3947 defines the NAT-T Vendor ID as literally MD5("RFC 3947"), a capability tag, not a security primitive.
	"encoding/binary"
	"net"
)

// RFC3947VendorID announces support for standardized NAT-Traversal
// (RFC 3947 §7 defines the Vendor ID payload's content as exactly the MD5
// digest of the ASCII string "RFC 3947" — this is a capability tag agreed
// by every RFC-3947-compliant implementation, not a secret or a homemade
// hash construction).
func RFC3947VendorID() []byte {
	sum := md5.Sum([]byte("RFC 3947"))
	return sum[:]
}

// computeNATD is RFC 3947 §4's NAT-D payload: HASH(CKY-I | CKY-R | Address | Port),
// using the Phase 1 hash algorithm already negotiated, over one candidate
// (address, port) pair. The initiator sends one NAT-D for its own address
// and one for what it believes the responder's address is; the responder
// does the mirror image. A mismatch on either side reveals a NAT between
// that endpoint and the peer.
func computeNATD(hashAlg int, initiatorSPI, responderSPI [8]byte, ip net.IP, port uint16) ([]byte, error) {
	ip4 := ip.To4()
	if ip4 == nil {
		ip4 = ip.To16()
	}
	buf := make([]byte, 0, 16+len(ip4)+2)
	buf = append(buf, initiatorSPI[:]...)
	buf = append(buf, responderSPI[:]...)
	buf = append(buf, ip4...)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, port)
	buf = append(buf, portBytes...)
	return digest(hashAlg, buf)
}
