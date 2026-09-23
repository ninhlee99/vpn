// Package ipsec implements ESP (RFC 4303) in transport mode with UDP
// encapsulation (RFC 3948) — the data-plane counterpart to internal/ike's
// Quick Mode SA negotiation. This client only ever protects UDP/1701
// (L2TP) traffic, matching entrypoint.sh's `type=transport` config. The
// cipher and integrity transform are whatever Quick Mode negotiated.
package ipsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"math"
)

// Cipher is the ESP encryption transform of an SA.
type Cipher int

const (
	Cipher3DESCBC Cipher = iota + 1 // RFC 2451, 24-byte key
	CipherAESCBC                    // RFC 3602, 16/24/32-byte key
)

// Integrity is the ESP authentication transform of an SA.
type Integrity int

const (
	IntegHMACSHA1_96    Integrity = iota + 1 // RFC 2404, 20-byte key, 12-byte ICV
	IntegHMACSHA256_128                      // RFC 4868, 32-byte key, 16-byte ICV
)

// SA is one direction's ESP security association — mirrors ike.ChildSA but
// lives in this package so ipsec doesn't import ike (keeps the dependency
// direction one-way: engine wires ike's output into ipsec, not the reverse).
// Build it with NewSA, which validates the keys against the transforms.
type SA struct {
	SPI       uint32
	Cipher    Cipher
	Integrity Integrity
	EncKey    []byte
	AuthKey   []byte

	block   cipher.Block
	newHash func() hash.Hash
	icvLen  int

	seq        uint32 // outbound: last sequence number sent
	replaySeen [64]bool
	replayBase uint32
	replayInit bool
}

// NewSA builds an SA for the negotiated transforms, rejecting keys whose
// length doesn't fit them — a mismatch here means the IKE layer and this
// one disagree about what was negotiated, which must never be papered over
// by running the wrong cipher.
func NewSA(spi uint32, c Cipher, i Integrity, encKey, authKey []byte) (*SA, error) {
	sa := &SA{SPI: spi, Cipher: c, Integrity: i, EncKey: encKey, AuthKey: authKey}
	var err error
	switch c {
	case Cipher3DESCBC:
		sa.block, err = des.NewTripleDESCipher(encKey)
	case CipherAESCBC:
		sa.block, err = aes.NewCipher(encKey)
	default:
		return nil, fmt.Errorf("unsupported ESP cipher %d", c)
	}
	if err != nil {
		return nil, fmt.Errorf("ESP cipher init: %w", err)
	}
	var keyLen int
	switch i {
	case IntegHMACSHA1_96:
		sa.newHash, keyLen, sa.icvLen = sha1.New, sha1.Size, 12
	case IntegHMACSHA256_128:
		sa.newHash, keyLen, sa.icvLen = sha256.New, sha256.Size, 16
	default:
		return nil, fmt.Errorf("unsupported ESP integrity algorithm %d", i)
	}
	if len(authKey) != keyLen {
		return nil, fmt.Errorf("ESP integrity key is %d bytes, want %d", len(authKey), keyLen)
	}
	return sa, nil
}

// Encrypt wraps one IP payload (the UDP/1701 L2TP datagram, without its own
// IP header — transport mode replaces only what ESP replaces) into an ESP
// packet, RFC 4303 format: SPI | Seq | IV | ciphertext(payload | pad | pad-len | next-header) | ICV.
// nextHeader is the IP protocol number of payload (17 for UDP).
func (sa *SA) Encrypt(payload []byte, nextHeader byte) ([]byte, error) {
	// RFC 4303 §3.3.3: the sequence number must never cycle within one SA.
	if sa.seq == math.MaxUint32 {
		return nil, fmt.Errorf("ESP sequence number exhausted — SA must be rekeyed")
	}
	sa.seq++
	seq := sa.seq

	blockLen := sa.block.BlockSize()
	iv := make([]byte, blockLen)
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}

	// Pad so payload+pad+padLen+nextHeader is a multiple of the block size,
	// RFC 4303 §2.4. Pad bytes are 1,2,3,... (a standard, verifiable filler
	// — not security-relevant, just alignment).
	total := len(payload) + 2 // + pad-length byte + next-header byte
	padNeeded := (blockLen - (total % blockLen)) % blockLen
	plain := make([]byte, 0, len(payload)+padNeeded+2)
	plain = append(plain, payload...)
	for i := 0; i < padNeeded; i++ {
		plain = append(plain, byte(i+1))
	}
	plain = append(plain, byte(padNeeded), nextHeader)

	out := make([]byte, 8+blockLen+len(plain)+sa.icvLen)
	binary.BigEndian.PutUint32(out[0:4], sa.SPI)
	binary.BigEndian.PutUint32(out[4:8], seq)
	copy(out[8:8+blockLen], iv)
	body := out[8+blockLen : 8+blockLen+len(plain)]
	cipher.NewCBCEncrypter(sa.block, iv).CryptBlocks(body, plain)

	icvOffset := 8 + blockLen + len(plain)
	mac := hmac.New(sa.newHash, sa.AuthKey)
	mac.Write(out[:icvOffset]) // ICV covers SPI|Seq|IV|ciphertext, RFC 4303
	copy(out[icvOffset:], mac.Sum(nil)[:sa.icvLen])
	return out, nil
}

// Decrypt reverses Encrypt: verifies the ICV, checks the replay window,
// decrypts, strips padding, and returns the inner payload plus its
// next-header protocol number.
func (sa *SA) Decrypt(pkt []byte) (payload []byte, nextHeader byte, err error) {
	blockLen := sa.block.BlockSize()
	if len(pkt) < 8+blockLen+blockLen+sa.icvLen {
		return nil, 0, fmt.Errorf("ESP packet too short: %d bytes", len(pkt))
	}
	spi := binary.BigEndian.Uint32(pkt[0:4])
	if spi != sa.SPI {
		return nil, 0, fmt.Errorf("ESP SPI mismatch: got %08x want %08x", spi, sa.SPI)
	}
	seq := binary.BigEndian.Uint32(pkt[4:8])
	if err := sa.checkReplay(seq); err != nil {
		return nil, 0, err
	}

	icvOffset := len(pkt) - sa.icvLen
	mac := hmac.New(sa.newHash, sa.AuthKey)
	mac.Write(pkt[:icvOffset])
	if !hmac.Equal(mac.Sum(nil)[:sa.icvLen], pkt[icvOffset:]) {
		return nil, 0, fmt.Errorf("ESP ICV verification failed (wrong key, or corrupted/tampered packet)")
	}

	iv := pkt[8 : 8+blockLen]
	ciphertext := pkt[8+blockLen : icvOffset]
	if len(ciphertext)%blockLen != 0 {
		return nil, 0, fmt.Errorf("ESP ciphertext length %d not a multiple of block size %d", len(ciphertext), blockLen)
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(sa.block, iv).CryptBlocks(plain, ciphertext)

	padLen := int(plain[len(plain)-2])
	nextHeader = plain[len(plain)-1]
	if padLen+2 > len(plain) {
		return nil, 0, fmt.Errorf("ESP padding length %d exceeds plaintext", padLen)
	}
	payload = plain[:len(plain)-2-padLen]
	sa.markSeqSeen(seq)
	return payload, nextHeader, nil
}

// checkReplay implements a standard sliding replay window (RFC 4303 §3.4.3):
// a sequence number older than the window, or already seen, is rejected.
func (sa *SA) checkReplay(seq uint32) error {
	if seq == 0 {
		return fmt.Errorf("ESP replay check: sequence number 0 is invalid")
	}
	if !sa.replayInit {
		sa.replayBase = seq
		sa.replayInit = true
		return nil
	}
	if seq > sa.replayBase {
		return nil // advances the window; markSeqSeen shifts it
	}
	diff := sa.replayBase - seq
	if diff >= uint32(len(sa.replaySeen)) {
		return fmt.Errorf("ESP replay check: sequence %d too old (window base %d)", seq, sa.replayBase)
	}
	if sa.replaySeen[diff] {
		return fmt.Errorf("ESP replay check: sequence %d already seen (replay)", seq)
	}
	return nil
}

func (sa *SA) markSeqSeen(seq uint32) {
	if !sa.replayInit {
		sa.replayBase = seq
		sa.replayInit = true
	}
	if seq > sa.replayBase {
		shift := seq - sa.replayBase
		if shift >= uint32(len(sa.replaySeen)) {
			sa.replaySeen = [64]bool{}
		} else {
			copy(sa.replaySeen[shift:], sa.replaySeen[:])
			for i := uint32(0); i < shift; i++ {
				sa.replaySeen[i] = false
			}
		}
		sa.replayBase = seq
		sa.replaySeen[0] = true
		return
	}
	diff := sa.replayBase - seq
	if diff < uint32(len(sa.replaySeen)) {
		sa.replaySeen[diff] = true
	}
}
