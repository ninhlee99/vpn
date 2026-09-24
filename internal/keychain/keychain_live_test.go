//go:build keychain_live

// Round-trips real secrets through the login Keychain. Opt-in only (it
// writes to the invoking user's Keychain): go test -tags keychain_live ./internal/keychain
package keychain

import "testing"

func TestSetGetRoundTripLive(t *testing.T) {
	const profile = "selftest-probe"
	secrets := []string{
		"plain",
		`a b"c\d'e`,
		`trailing\`,
		`$HOME %s ;|&`,
		"unicode-mật-khẩu-🔑",
		" leading and trailing spaces ",
		"0xdeadbeef",
		"",
	}
	t.Cleanup(func() { _ = DeletePSK(profile) })
	for _, want := range secrets {
		if err := SetPSK(profile, want); err != nil {
			t.Fatalf("SetPSK(%q): %v", want, err)
		}
		got, err := GetPSK(profile)
		if err != nil {
			t.Fatalf("GetPSK after SetPSK(%q): %v", want, err)
		}
		if got != want {
			t.Fatalf("round trip: got %q want %q", got, want)
		}
	}
	if err := SetPSK(profile, "line\nbreak"); err == nil {
		t.Fatal("SetPSK accepted a secret containing a newline")
	}
}
