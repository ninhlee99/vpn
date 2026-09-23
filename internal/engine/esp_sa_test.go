package engine

import (
	"testing"

	"vpn/internal/ike"
	"vpn/internal/ipsec"
)

func TestNewESPSAUsesNegotiatedTransform(t *testing.T) {
	cases := []struct {
		tr        ike.Transform
		enc, auth int
		cipher    ipsec.Cipher
		integrity ipsec.Integrity
	}{
		{ike.Transform{Encryption: ike.Enc3DES, Hash: ike.HashSHA1}, 24, 20, ipsec.Cipher3DESCBC, ipsec.IntegHMACSHA1_96},
		{ike.Transform{Encryption: ike.EncAES, KeyBits: 128, Hash: ike.HashSHA1}, 16, 20, ipsec.CipherAESCBC, ipsec.IntegHMACSHA1_96},
		{ike.Transform{Encryption: ike.EncAES, KeyBits: 256, Hash: ike.HashSHA256}, 32, 32, ipsec.CipherAESCBC, ipsec.IntegHMACSHA256_128},
	}
	for _, c := range cases {
		sa, err := newESPSA(ike.ChildSA{SPI: 1, EncKey: make([]byte, c.enc), AuthKey: make([]byte, c.auth), Transform: c.tr})
		if err != nil {
			t.Fatalf("%+v: %v", c.tr, err)
		}
		if sa.Cipher != c.cipher || sa.Integrity != c.integrity {
			t.Fatalf("%+v: got cipher %d integrity %d", c.tr, sa.Cipher, sa.Integrity)
		}
	}
	if _, err := newESPSA(ike.ChildSA{SPI: 1, EncKey: make([]byte, 8), AuthKey: make([]byte, 20), Transform: ike.Transform{Encryption: ike.EncDES, Hash: ike.HashSHA1}}); err == nil {
		t.Fatal("single DES accepted")
	}
}
