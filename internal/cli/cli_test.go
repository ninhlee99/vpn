package cli

import "testing"

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
