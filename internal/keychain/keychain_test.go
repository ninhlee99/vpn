package keychain

import "testing"

// Lines captured from real `security find-generic-password -g` output on
// macOS 14 (see keychain_live_test.go for the round trip that produced them).
func TestParsePasswordLine(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{`password: "plain"`, "plain"},
		{`password: "756e69"`, "756e69"},
		{`password: 0x756E69636F64652D6DE1BAAD742DF09F9491  "unicode-m\341\272\255t-\360\237\224\221"`, "unicode-mật-🔑"},
		{`password: 0x6122625C63  "a"b\134c"`, `a"b\c`},
		{`password: `, ""},
	}
	for _, c := range cases {
		got, ok, err := parsePasswordLine(c.line)
		if !ok || err != nil {
			t.Fatalf("parsePasswordLine(%q): ok=%v err=%v", c.line, ok, err)
		}
		if got != c.want {
			t.Fatalf("parsePasswordLine(%q) = %q, want %q", c.line, got, c.want)
		}
	}
	if _, ok, _ := parsePasswordLine(`keychain: "/Users/x/Library/Keychains/login.keychain-db"`); ok {
		t.Fatal("non-password line was treated as the password")
	}
}

func TestInteractiveCommandEscaping(t *testing.T) {
	got, err := interactiveCommand("add-generic-password", "-w", `a"b\c`)
	if err != nil {
		t.Fatal(err)
	}
	want := `"add-generic-password" "-w" "a\"b\\c"` + "\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if _, err := interactiveCommand("x\ny"); err == nil {
		t.Fatal("newline was accepted")
	}
}
