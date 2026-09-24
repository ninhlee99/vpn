package ppp

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// Official RFC 2759 §9.2 "Hash Example" test vector, fetched from
// https://www.rfc-editor.org/rfc/rfc2759.txt and cross-verified via two
// independent fetches — not hand-transcribed from memory (see the
// commit/PR notes on the DH-prime incident this test exists to avoid
// repeating).
func TestMSCHAPv2RFC2759Vector(t *testing.T) {
	username := "User"
	password := "clientPass"
	authenticatorChallenge := mustHex(t, "5B5D7C7D7B3F2F3E3C2C60213226 2628")
	peerChallenge := mustHex(t, "21402324255E262A28295F2B3A337C7E")

	wantChallenge := mustHex(t, "D02E4386BCE91226")
	wantPasswordHash := mustHex(t, "44EBBA8D5312B8D611474411F56989AE")
	wantNTResponse := mustHex(t, "82309ECD8D708B5EA08FAA3981CD8354"+"4233114A3D85D6DF")

	gotChallenge, err := challengeHash(peerChallenge, authenticatorChallenge, username)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotChallenge, wantChallenge) {
		t.Fatalf("ChallengeHash: got %x want %x", gotChallenge, wantChallenge)
	}

	gotPasswordHash, err := ntPasswordHash(password)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotPasswordHash, wantPasswordHash) {
		t.Fatalf("NtPasswordHash: got %x want %x", gotPasswordHash, wantPasswordHash)
	}

	gotNTResponse, err := challengeResponse(gotChallenge, gotPasswordHash)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotNTResponse, wantNTResponse) {
		t.Fatalf("ChallengeResponse: got %x want %x", gotNTResponse, wantNTResponse)
	}
}

// RFC 2759 §9.2 AuthenticatorResponse for the same inputs as above, run
// through the production path (generateMSCHAPv2Response + VerifySuccess).
func TestMSCHAPv2AuthenticatorResponseRFC2759Vector(t *testing.T) {
	var peerChallenge [16]byte
	copy(peerChallenge[:], mustHex(t, "21402324255E262A28295F2B3A337C7E"))
	authenticatorChallenge := mustHex(t, "5B5D7C7D7B3F2F3E3C2C602132262628")

	r, err := generateMSCHAPv2Response(peerChallenge, authenticatorChallenge, "User", "clientPass")
	if err != nil {
		t.Fatal(err)
	}
	if want := mustHex(t, "407A5589115FD0D6209F510FE9C04566932CDA56"); !bytes.Equal(r.expectedAuthenticator[:], want) {
		t.Fatalf("AuthenticatorResponse: got %X want %X", r.expectedAuthenticator, want)
	}

	if err := r.VerifySuccess([]byte("S=407A5589115FD0D6209F510FE9C04566932CDA56 M=Access granted")); err != nil {
		t.Fatalf("genuine Success rejected: %v", err)
	}
	if err := r.VerifySuccess([]byte("S=407a5589115fd0d6209f510fe9c04566932cda56")); err != nil {
		t.Fatalf("lowercase hex Success rejected: %v", err)
	}
	for _, bad := range []string{
		"",
		"M=Access granted",
		"S=0000000000000000000000000000000000000000 M=Access granted",
		"S=407A5589115FD0D6209F510FE9C0456693",
		"S=ZZ7A5589115FD0D6209F510FE9C04566932CDA56",
	} {
		if err := r.VerifySuccess([]byte(bad)); err == nil {
			t.Fatalf("forged Success %q accepted", bad)
		}
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	clean := make([]byte, 0, len(s))
	for _, c := range []byte(s) {
		if c != ' ' {
			clean = append(clean, c)
		}
	}
	b, err := hex.DecodeString(string(clean))
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}
