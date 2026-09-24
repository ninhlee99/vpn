package ipsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"math"
	"testing"
)

type suite struct {
	cipher    Cipher
	integrity Integrity
	encLen    int
	authLen   int
	icvLen    int
}

var suites = []suite{
	{Cipher3DESCBC, IntegHMACSHA1_96, 24, 20, 12},
	{CipherAESCBC, IntegHMACSHA1_96, 16, 20, 12},
	{CipherAESCBC, IntegHMACSHA256_128, 32, 32, 16},
}

func (s suite) String() string {
	return fmt.Sprintf("cipher%d-integ%d-%d", s.cipher, s.integrity, s.encLen*8)
}

func (s suite) pair(t *testing.T) (out, in *SA) {
	t.Helper()
	enc := bytes.Repeat([]byte{0x11}, s.encLen)
	auth := bytes.Repeat([]byte{0x22}, s.authLen)
	out, err := NewSA(0xAABBCCDD, s.cipher, s.integrity, enc, auth)
	if err != nil {
		t.Fatal(err)
	}
	in, err = NewSA(0xAABBCCDD, s.cipher, s.integrity, enc, auth)
	if err != nil {
		t.Fatal(err)
	}
	return out, in
}

func TestESPRoundTripAllSuites(t *testing.T) {
	for _, s := range suites {
		t.Run(s.String(), func(t *testing.T) {
			out, in := s.pair(t)
			for n := 0; n < 40; n++ { // every padding length for both block sizes
				payload := bytes.Repeat([]byte{byte(n)}, n)
				pkt, err := out.Encrypt(payload, 17)
				if err != nil {
					t.Fatal(err)
				}
				got, nextHeader, err := in.Decrypt(pkt)
				if err != nil {
					t.Fatalf("len %d: %v", n, err)
				}
				if nextHeader != 17 || !bytes.Equal(got, payload) {
					t.Fatalf("len %d: got %x/%d", n, got, nextHeader)
				}
			}
		})
	}
}

// The ICV must be the RFC-specified truncation of HMAC over
// SPI|Seq|IV|ciphertext — checked independently of the SA's own code.
func TestESPICVIsTruncatedHMAC(t *testing.T) {
	s := suites[2] // HMAC-SHA256-128
	out, _ := s.pair(t)
	pkt, err := out.Encrypt([]byte("payload"), 17)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, out.AuthKey)
	mac.Write(pkt[:len(pkt)-16])
	if !bytes.Equal(mac.Sum(nil)[:16], pkt[len(pkt)-16:]) {
		t.Fatal("ICV is not HMAC-SHA256 truncated to 128 bits (RFC 4868)")
	}
}

func TestESPWrongKeyFailsICV(t *testing.T) {
	for _, s := range suites {
		out, _ := s.pair(t)
		in, err := NewSA(out.SPI, s.cipher, s.integrity, out.EncKey, bytes.Repeat([]byte{0x33}, s.authLen))
		if err != nil {
			t.Fatal(err)
		}
		pkt, _ := out.Encrypt([]byte("data"), 17)
		if _, _, err := in.Decrypt(pkt); err == nil {
			t.Fatalf("%s: wrong auth key accepted", s)
		}
	}
}

func TestESPReplayRejected(t *testing.T) {
	out, in := suites[0].pair(t)
	pkt, _ := out.Encrypt([]byte("data"), 17)
	if _, _, err := in.Decrypt(pkt); err != nil {
		t.Fatalf("first decrypt should succeed: %v", err)
	}
	if _, _, err := in.Decrypt(pkt); err == nil {
		t.Fatal("expected replay rejection on second decrypt of the same packet")
	}
}

func TestNewSARejectsMismatchedKeys(t *testing.T) {
	cases := []struct {
		c       Cipher
		i       Integrity
		enc, au int
	}{
		{Cipher3DESCBC, IntegHMACSHA1_96, 16, 20},   // AES-128 key fed to 3DES
		{CipherAESCBC, IntegHMACSHA1_96, 20, 20},    // not an AES key size
		{CipherAESCBC, IntegHMACSHA256_128, 32, 20}, // SHA-1 sized key for SHA-256
		{CipherAESCBC, Integrity(99), 16, 20},       // unknown integrity
		{Cipher(99), IntegHMACSHA1_96, 16, 20},      // unknown cipher
	}
	for _, c := range cases {
		if _, err := NewSA(1, c.c, c.i, make([]byte, c.enc), make([]byte, c.au)); err == nil {
			t.Errorf("NewSA(%d,%d,enc=%d,auth=%d) accepted", c.c, c.i, c.enc, c.au)
		}
	}
}

func TestESPSequenceNeverCycles(t *testing.T) {
	out, _ := suites[0].pair(t)
	out.seq = math.MaxUint32
	if _, err := out.Encrypt([]byte("x"), 17); err == nil {
		t.Fatal("sequence number wrapped instead of failing")
	}
}
