package ppp

import (
	"crypto/des"
	"crypto/rand"
	"crypto/sha1"
	"fmt"
	"unicode/utf16"

	"golang.org/x/crypto/md4" //nolint:staticcheck // MD4 is mandated by MS-CHAPv2 (RFC 2759) for the legacy NT password hash — not our choice, and not used for anything but interop with this specific auth protocol.
)

// MSCHAPv2Response is the 49-byte Response field of a CHAP Response packet
// when Algorithm = CHAPAlgoMSCHAPv2 (RFC 2759 §8.1).
type MSCHAPv2Response struct {
	PeerChallenge [16]byte
	NTResponse    [24]byte
	Flags         uint8 // always 0, RFC 2759 §8.1
}

// GenerateMSCHAPv2Response computes the full response to an MS-CHAPv2
// challenge from the LNS, RFC 2759 §4-§8: ChallengeHash, then
// NtPasswordHash, then the triple-DES ChallengeResponse.
func GenerateMSCHAPv2Response(authenticatorChallenge []byte, username, password string) (*MSCHAPv2Response, error) {
	var peerChallenge [16]byte
	if _, err := rand.Read(peerChallenge[:]); err != nil {
		return nil, fmt.Errorf("generate PeerChallenge: %w", err)
	}

	challenge, err := challengeHash(peerChallenge[:], authenticatorChallenge, username)
	if err != nil {
		return nil, err
	}
	passwordHash, err := ntPasswordHash(password)
	if err != nil {
		return nil, err
	}
	ntResponse, err := challengeResponse(challenge, passwordHash)
	if err != nil {
		return nil, err
	}

	r := &MSCHAPv2Response{PeerChallenge: peerChallenge}
	copy(r.NTResponse[:], ntResponse)
	return r, nil
}

// Marshal encodes the 49-byte Response field: PeerChallenge(16) |
// Reserved(8, zero) | NTResponse(24) | Flags(1), RFC 2759 §8.1.
func (r *MSCHAPv2Response) Marshal() []byte {
	b := make([]byte, 49)
	copy(b[0:16], r.PeerChallenge[:])
	// b[16:24] Reserved, zero
	copy(b[24:48], r.NTResponse[:])
	b[48] = r.Flags
	return b
}

// challengeHash is RFC 2759 §8.2's ChallengeHash: SHA1(PeerChallenge |
// AuthenticatorChallenge | Username), truncated to the first 8 bytes. Only
// the username's account-name portion is used if it contains a domain
// (RFC 2759 doesn't specify domain stripping explicitly, but every real
// implementation strips a "DOMAIN\" prefix if present — this client's
// account names from the reference config never include one, so this is a
// no-op here, kept only for correctness if a future profile's username
// does).
func challengeHash(peerChallenge, authenticatorChallenge []byte, username string) ([]byte, error) {
	if i := indexByte(username, '\\'); i >= 0 {
		username = username[i+1:]
	}
	h := sha1.New()
	h.Write(peerChallenge)
	h.Write(authenticatorChallenge)
	h.Write([]byte(username))
	sum := h.Sum(nil)
	return sum[:8], nil
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// ntPasswordHash is RFC 2759 §8.3's NtPasswordHash: MD4 of the password
// encoded as UTF-16LE (matching how Windows stores the NT hash — this is
// mandated by the protocol, not a choice of character encoding made here).
func ntPasswordHash(password string) ([]byte, error) {
	u16 := utf16.Encode([]rune(password))
	buf := make([]byte, len(u16)*2)
	for i, v := range u16 {
		buf[2*i] = byte(v)
		buf[2*i+1] = byte(v >> 8)
	}
	h := md4.New()
	h.Write(buf)
	return h.Sum(nil), nil
}

// challengeResponse is RFC 2759 §8.5's ChallengeResponse: the 16-byte NT
// password hash, zero-padded to 21 bytes and split into three 7-byte DES
// keys, each used to DES-encrypt the 8-byte Challenge; the three 8-byte
// outputs concatenate to the 24-byte NTResponse.
func challengeResponse(challenge, passwordHash []byte) ([]byte, error) {
	zHash := make([]byte, 21)
	copy(zHash, passwordHash)

	out := make([]byte, 24)
	for i := 0; i < 3; i++ {
		key56 := zHash[i*7 : i*7+7]
		key64 := expandDESKey(key56)
		block, err := des.NewCipher(key64)
		if err != nil {
			return nil, fmt.Errorf("DES key setup: %w", err)
		}
		block.Encrypt(out[i*8:i*8+8], challenge)
	}
	return out, nil
}

// expandDESKey spreads a 56-bit (7-byte) key across 8 bytes with the low
// bit of each byte left as a DES parity placeholder — Go's crypto/des does
// not check parity, so its value is irrelevant to the encryption result;
// this is the same bit layout every MS-CHAP implementation uses to turn a
// 7-byte key into a DES key.
func expandDESKey(k7 []byte) []byte {
	k8 := make([]byte, 8)
	k8[0] = k7[0]
	k8[1] = k7[0]<<7 | k7[1]>>1
	k8[2] = k7[1]<<6 | k7[2]>>2
	k8[3] = k7[2]<<5 | k7[3]>>3
	k8[4] = k7[3]<<4 | k7[4]>>4
	k8[5] = k7[4]<<3 | k7[5]>>5
	k8[6] = k7[5]<<2 | k7[6]>>6
	k8[7] = k7[6] << 1
	return k8
}
