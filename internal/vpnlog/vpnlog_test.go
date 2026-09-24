package vpnlog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotate(t *testing.T) {
	dir := t.TempDir()
	path, rotated := filepath.Join(dir, "vpn.log"), filepath.Join(dir, "vpn.log.1")

	os.WriteFile(path, make([]byte, 10), 0o600)
	rotate(path, rotated, 10)
	if _, err := os.Stat(rotated); !os.IsNotExist(err) {
		t.Fatal("rotated a file that is not over the limit")
	}

	os.WriteFile(path, make([]byte, 11), 0o600)
	os.WriteFile(rotated, []byte("older"), 0o600)
	rotate(path, rotated, 10)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("oversized log was not moved aside")
	}
	if fi, err := os.Stat(rotated); err != nil || fi.Size() != 11 {
		t.Fatalf("rotated file should be the 11-byte log, got %v %v", fi, err)
	}
}

func TestFieldsStringIsSortedAndRedacted(t *testing.T) {
	got := Fields{"payload_len": 72, "next_header": 17, "password": "hunter2"}.String()
	if want := " next_header=17 password=[REDACTED] payload_len=72"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
