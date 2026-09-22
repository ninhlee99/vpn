package ike

import (
	"bytes"
	"testing"
)

func TestCBCRoundTrip3DES(t *testing.T) {
	tr := Transform{Encryption: Enc3DES, Hash: HashSHA1}
	key := bytes.Repeat([]byte{0x11}, 24)
	iv := bytes.Repeat([]byte{0x22}, 8)
	plain := []byte("0123456789abcdef") // 16 bytes, multiple of 8
	ct, err := cbcEncrypt(tr, key, iv, plain)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := cbcDecrypt(tr, key, iv, ct)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pt, plain) {
		t.Fatalf("round trip mismatch: got %x want %x", pt, plain)
	}
}

func TestExpandKeyMatchesManualK1K2(t *testing.T) {
	key := []byte("testkey")
	got, err := expandKey(HashSHA1, key, 30) // > 20 bytes forces K1|K2 expansion
	if err != nil {
		t.Fatal(err)
	}
	k1, _ := prf(HashSHA1, key, []byte{0})
	k2, _ := prf(HashSHA1, key, k1)
	want := append(append([]byte{}, k1...), k2...)[:30]
	if !bytes.Equal(got, want) {
		t.Fatalf("expandKey mismatch: got %x want %x", got, want)
	}
}

func TestFullPhase1KeyDerivationSelfConsistent(t *testing.T) {
	// Not a known-answer test (no public IKEv1 KAT with these exact inputs
	// readily available) — this instead checks the derivation is at least
	// internally deterministic and produces a usable key/IV pair, and
	// exercises the same code path as the live client end to end.
	tr := Transform{Encryption: Enc3DES, Hash: HashSHA1, Group: 2}
	psk := []byte("sharedsecret")
	ni := bytes.Repeat([]byte{0xAA}, 32)
	nr := bytes.Repeat([]byte{0xBB}, 32)
	gxy := bytes.Repeat([]byte{0xCC}, 128)
	var ckyI, ckyR [8]byte
	copy(ckyI[:], []byte{1, 2, 3, 4, 5, 6, 7, 8})
	copy(ckyR[:], []byte{8, 7, 6, 5, 4, 3, 2, 1})

	k1, err := DerivePhase1Keys(tr, psk, ni, nr, gxy, ckyI, ckyR)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := DerivePhase1Keys(tr, psk, ni, nr, gxy, ckyI, ckyR)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(k1.EncKey, k2.EncKey) {
		t.Fatal("derivation is not deterministic")
	}
	if len(k1.EncKey) != 24 {
		t.Fatalf("3DES key should be 24 bytes, got %d", len(k1.EncKey))
	}

	gxi := bytes.Repeat([]byte{0xDD}, 128)
	gxr := bytes.Repeat([]byte{0xEE}, 128)
	saBody := []byte("fake-sa-body")
	idBody := MarshalIPv4ID([]byte{10, 0, 0, 1})
	hashI, err := k1.ComputeHashI(gxi, gxr, ckyI, ckyR, saBody, idBody)
	if err != nil {
		t.Fatal(err)
	}
	if len(hashI) != 20 {
		t.Fatalf("SHA1 HASH_I should be 20 bytes, got %d", len(hashI))
	}
}
