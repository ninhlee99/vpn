package ipsec

import (
	"bytes"
	"testing"
)

func testSA() (*SA, *SA) {
	enc := bytes.Repeat([]byte{0x11}, 24)
	auth := bytes.Repeat([]byte{0x22}, 20)
	out := &SA{SPI: 0xAABBCCDD, EncKey: enc, AuthKey: auth}
	in := &SA{SPI: 0xAABBCCDD, EncKey: enc, AuthKey: auth}
	return out, in
}

func TestESPRoundTrip(t *testing.T) {
	out, in := testSA()
	payload := []byte("hello l2tp over esp, this is a test udp payload")

	pkt, err := out.Encrypt(payload, 17)
	if err != nil {
		t.Fatal(err)
	}
	got, nextHeader, err := in.Decrypt(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if nextHeader != 17 {
		t.Errorf("next header = %d, want 17", nextHeader)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("decrypted payload mismatch: got %q want %q", got, payload)
	}
}

func TestESPWrongKeyFailsICV(t *testing.T) {
	out, in := testSA()
	in.AuthKey = bytes.Repeat([]byte{0x33}, 20)
	pkt, err := out.Encrypt([]byte("data"), 17)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := in.Decrypt(pkt); err == nil {
		t.Fatal("expected ICV verification failure with wrong auth key")
	}
}

func TestESPReplayRejected(t *testing.T) {
	out, in := testSA()
	pkt, err := out.Encrypt([]byte("data"), 17)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := in.Decrypt(pkt); err != nil {
		t.Fatalf("first decrypt should succeed: %v", err)
	}
	if _, _, err := in.Decrypt(pkt); err == nil {
		t.Fatal("expected replay rejection on second decrypt of the same packet")
	}
}

func TestESPMultiplePacketsInOrder(t *testing.T) {
	out, in := testSA()
	for i := 0; i < 10; i++ {
		payload := []byte{byte(i), byte(i), byte(i)}
		pkt, err := out.Encrypt(payload, 17)
		if err != nil {
			t.Fatal(err)
		}
		got, _, err := in.Decrypt(pkt)
		if err != nil {
			t.Fatalf("packet %d: %v", i, err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("packet %d: mismatch", i)
		}
	}
}
