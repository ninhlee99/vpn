package vpnlog

import (
	"os"
	"path/filepath"
	"strings"
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

func TestCapWriterTruncatesInPlace(t *testing.T) {
	f, err := os.OpenFile(filepath.Join(t.TempDir(), "vpn.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := &capWriter{f: f, limit: 82}
	if _, err := w.Write(make([]byte, 80)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("tail\n")); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(f.Name())
	if len(data) >= 80 || !strings.Contains(string(data), "size cap") || !strings.HasSuffix(string(data), "tail\n") {
		t.Fatalf("log was not emptied in place with a marker: %q", data)
	}
}
