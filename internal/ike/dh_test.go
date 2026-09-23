package ike

import (
	"math/big"
	"testing"
)

// A responder that sends 0, 1 or p-1 forces a shared secret it can predict
// without holding a private exponent, so key agreement must reject it
// instead of deriving keys from it.
func TestSharedSecretRejectsDegeneratePeerValues(t *testing.T) {
	group := Groups[2]
	kp, err := GenerateKeyPair(group)
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	size := group.BitLen / 8

	cases := map[string]*big.Int{
		"zero":      big.NewInt(0),
		"one":       big.NewInt(1),
		"p minus 1": new(big.Int).Sub(group.Prime, big.NewInt(1)),
		"p":         group.Prime,
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := kp.SharedSecret(leftPad(value.Bytes(), size)); err == nil {
				t.Fatalf("SharedSecret accepted peer value %s", name)
			}
		})
	}
}

func TestSharedSecretRejectsWrongLength(t *testing.T) {
	kp, err := GenerateKeyPair(Groups[2])
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	if _, err := kp.SharedSecret([]byte{1, 2, 3}); err == nil {
		t.Fatal("SharedSecret accepted a short KE payload")
	}
}

// Both sides must still agree on the same secret for a normal exchange.
func TestSharedSecretAgrees(t *testing.T) {
	a, err := GenerateKeyPair(Groups[2])
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	b, err := GenerateKeyPair(Groups[2])
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	fromA, err := a.SharedSecret(b.PublicBytes())
	if err != nil {
		t.Fatalf("a.SharedSecret: %v", err)
	}
	fromB, err := b.SharedSecret(a.PublicBytes())
	if err != nil {
		t.Fatalf("b.SharedSecret: %v", err)
	}
	if string(fromA) != string(fromB) {
		t.Fatal("peers derived different shared secrets")
	}
}
