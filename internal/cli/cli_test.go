package cli

import (
	"testing"

	"vpn/internal/config"
)

func TestFlagsFirstAllowsDocumentedProfileSyntax(t *testing.T) {
	got := flagsFirst(
		[]string{"work", "--server", "vpn.example", "--mtu", "1300", "--full-tunnel"},
		map[string]bool{"--server": true, "--server-id": true, "--mtu": true, "--psk": true},
	)
	want := []string{"--server", "vpn.example", "--mtu", "1300", "--full-tunnel", "work"}
	if len(got) != len(want) {
		t.Fatalf("flagsFirst() = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("flagsFirst() = %q, want %q", got, want)
		}
	}
}

func TestFlagsFirstAllowsDocumentedAccountSyntax(t *testing.T) {
	got := flagsFirst(
		[]string{"office", "alice", "--password", "secret", "--default"},
		map[string]bool{"--password": true},
	)
	want := []string{"--password", "secret", "--default", "office", "alice"}
	if len(got) != len(want) {
		t.Fatalf("flagsFirst() = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("flagsFirst() = %q, want %q", got, want)
		}
	}
}

func TestRunInvalidFlagReturnsError(t *testing.T) {
	if code := Run([]string{"status", "--unknown"}); code != 1 {
		t.Fatalf("Run() exit code = %d, want 1", code)
	}
}

func TestFormatProfile(t *testing.T) {
	p := &config.Profile{
		Server:         "vpn.example",
		FullTunnel:     true,
		DefaultAccount: "bob",
		Accounts:       map[string]*config.Account{"bob": {Username: "bob"}, "amy": {Username: "amy"}},
	}
	want := "* work  server=vpn.example  full tunnel\n    account amy\n  * account bob\n"
	if got := formatProfile("work", p, true); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	empty := &config.Profile{Server: "x"}
	if got := formatProfile("lab", empty, false); got != "  lab  server=x  split tunnel\n    (no account — run `vpn account add lab <username> --default`)\n" {
		t.Fatalf("got:\n%q", got)
	}
}
