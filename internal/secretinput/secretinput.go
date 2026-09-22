// Package secretinput reads secrets (PSK, passwords) from a terminal without
// echoing them, so they never land in shell history or a process listing —
// unlike passing them as CLI flags.
package secretinput

import (
	"bufio"
	"fmt"
	"os"

	"golang.org/x/term"
)

// Prompt asks for a secret on stderr and reads it from stdin without echo
// when stdin is a terminal; falls back to a plain line read otherwise (e.g.
// piped input in scripts/tests).
func Prompt(label string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s: ", label)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	return line, nil
}
