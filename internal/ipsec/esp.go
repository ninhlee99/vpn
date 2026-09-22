// Package ipsec implements ESP (RFC 4303) in transport mode with UDP
// encapsulation (RFC 3948) — the data-plane counterpart to internal/ike's
// Quick Mode SA negotiation. This client only ever protects UDP/1701
// (L2TP) traffic, matching entrypoint.sh's `type=transport` config.
package ipsec

import (
	"crypto/cipher"
	"crypto/des"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
)

// SA is one direction's ESP security association — mirrors ike.ChildSA but
// lives in this package so ipsec doesn't import ike (keeps the dependency
// direction one-way: engine wires ike's output into ipsec, not the reverse).
type SA struct {
	SPI     uint32
	EncKey  []byte // 24 bytes for 3DES-CBC
	AuthKey []byte // 20 bytes for HMAC-SHA1-96

	seq        uint32 // outbound: next sequence number to send
	replaySeen [64]bool
	replayBase uint32
	replayInit bool
}

const (
	icvLen   = 12 // HMAC-SHA1-96, RFC 2404: the 20-byte HMAC is truncated to 96 bits
	blockLen = des.BlockSize
)

// Encrypt wraps one IP payload (the UDP/1701 L2TP datagram, without its own
// IP header — transport mode replaces only what ESP replaces) into an ESP
// packet, RFC 4303 format: SPI | Seq | IV | ciphertext(payload | pad | pad-len | next-header) | ICV.
// nextHeader is the IP protocol number of payload (17 for UDP).
func (sa *SA) Encrypt(payload []byte, nextHeader byte) ([]byte, error) {
	block, err := des.NewTripleDESCipher(sa.EncKey)
	if err != nil {
		return nil, fmt.Errorf("ESP cipher init: %w", err)
	}

	sa.seq++
	seq := sa.seq

	iv := make([]byte, blockLen)
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}

	// Pad so payload+pad+padLen+nextHeader is a multiple of the block size,
	// RFC 4303 §2.4. Pad bytes are 1,2,3,... (a standard, verifiable filler
	// — not security-relevant, just alignment).
	total := len(payload) + 2 // + pad-length byte + next-header byte
	padNeeded := (blockLen - (total % blockLen)) % blockLen
	pad := make([]byte, padNeeded)
	for i := range pad {
		pad[i] = byte(i + 1)
	}

	plain := make([]byte, 0, len(payload)+len(pad)+2)
	plain = append(plain, payload...)
	plain = append(plain, pad...)
	plain = append(plain, byte(padNeeded), nextHeader)

	ciphertext := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, plain)

	out := make([]byte, 8+blockLen+len(ciphertext)+icvLen)
	binary.BigEndian.PutUint32(out[0:4], sa.SPI)
	binary.BigEndian.PutUint32(out[4:8], seq)
	copy(out[8:8+blockLen], iv)
	copy(out[8+blockLen:8+blockLen+len(ciphertext)], ciphertext)

	mac := hmac.New(sha1.New, sa.AuthKey)
	mac.Write(out[:8+blockLen+len(ciphertext)]) // ICV covers SPI|Seq|IV|ciphertext, RFC 4303
	icv := mac.Sum(nil)
	copy(out[8+blockLen+len(ciphertext):], icv[:icvLen])

	return out, nil
}

// Decrypt reverses Encrypt: verifies the ICV, checks the replay window,
// decrypts, strips padding, and returns the inner payload plus its
// next-header protocol number.
func (sa *SA) Decrypt(pkt []byte) (payload []byte, nextHeader byte, err error) {
	if len(pkt) < 8+blockLen+icvLen {
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

	icvOffset := len(pkt) - icvLen
	mac := hmac.New(sha1.New, sa.AuthKey)
	mac.Write(pkt[:icvOffset])
	expected := mac.Sum(nil)[:icvLen]
	if !hmac.Equal(expected, pkt[icvOffset:]) {
		return nil, 0, fmt.Errorf("ESP ICV verification failed (wrong key, or corrupted/tampered packet)")
	}

	block, err := des.NewTripleDESCipher(sa.EncKey)
	if err != nil {
		return nil, 0, err
	}
	iv := pkt[8 : 8+blockLen]
	ciphertext := pkt[8+blockLen : icvOffset]
	if len(ciphertext)%blockLen != 0 || len(ciphertext) == 0 {
		return nil, 0, fmt.Errorf("ESP ciphertext length %d not a positive multiple of block size", len(ciphertext))
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)

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
