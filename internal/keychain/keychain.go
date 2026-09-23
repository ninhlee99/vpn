// Package keychain stores and retrieves VPN secrets (PSKs, account
// passwords) using the macOS login Keychain via the system `security` tool.
// This is the standard, documented way for a non-sandboxed CLI to use
// Keychain without linking Security.framework through cgo — no third-party
// dependency, no secret ever written to disk outside the Keychain itself.
package keychain

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

const servicePrefix = "vpn"

// Absolute path, not just "security" — this runs as root when called
// from an elevated context (Connect reads secrets while privilege.Elevate
// is raised), and a bare command name would be resolved via $PATH, which
// a local non-root user fully controls — classic setuid PATH hijacking.
const securityBin = "/usr/bin/security"

// pskService/passwordService namespace Keychain entries per profile/account
// so multiple VPN profiles and multiple accounts on the same profile never
// collide with each other or with unrelated Keychain items.
func pskService(profile string) string {
	return fmt.Sprintf("%s.psk.%s", servicePrefix, profile)
}

func passwordService(profile, account string) string {
	return fmt.Sprintf("%s.pwd.%s.%s", servicePrefix, profile, account)
}

// SetPSK stores (or overwrites) the IPsec pre-shared key for a profile.
func SetPSK(profile, psk string) error {
	return set(pskService(profile), profile, psk)
}

// GetPSK retrieves the IPsec pre-shared key for a profile.
func GetPSK(profile string) (string, error) {
	return get(pskService(profile), profile)
}

// DeletePSK removes the stored PSK for a profile, if any.
func DeletePSK(profile string) error {
	return delete_(pskService(profile), profile)
}

// SetPassword stores (or overwrites) an account's VPN login password.
func SetPassword(profile, account, password string) error {
	return set(passwordService(profile, account), account, password)
}

// GetPassword retrieves an account's VPN login password.
func GetPassword(profile, account string) (string, error) {
	return get(passwordService(profile, account), account)
}

// DeletePassword removes a stored account password, if any.
func DeletePassword(profile, account string) error {
	return delete_(passwordService(profile, account), account)
}

// Has reports whether a secret exists without retrieving its value.
func Has(service, account string) bool {
	cmd := exec.Command(securityBin, "find-generic-password", "-s", service, "-a", account)
	return cmd.Run() == nil
}

// set writes the secret through `security -i` (interactive mode, commands
// read from stdin) instead of `add-generic-password -w <secret>`: argv of a
// running process is visible to every local user via `ps`, stdin is not.
// -U: update in place if it already exists, instead of erroring.
func set(service, account, secret string) error {
	line, err := interactiveCommand("add-generic-password", "-U", "-s", service, "-a", account, "-w", secret)
	if err != nil {
		return err
	}
	cmd := exec.Command(securityBin, "-i")
	cmd.Stdin = strings.NewReader(line)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("store secret in Keychain: %w: %s", err, strings.TrimSpace(stderr.String()+" "+stdout.String()))
	}
	return nil
}

// interactiveCommand renders one `security -i` command line: every argument
// double-quoted, with `\` and `"` backslash-escaped (the escaping security's
// interactive parser understands). A newline cannot be represented inside a
// single command line, so it is rejected rather than silently truncating.
func interactiveCommand(args ...string) (string, error) {
	quoted := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, "\r\n") {
			return "", fmt.Errorf("Keychain values must not contain line breaks")
		}
		a = strings.ReplaceAll(a, `\`, `\\`)
		a = strings.ReplaceAll(a, `"`, `\"`)
		quoted[i] = `"` + a + `"`
	}
	return strings.Join(quoted, " ") + "\n", nil
}

// get uses -g (password line on stderr) rather than -w: -w prints the raw
// value for plain ASCII but silently switches to bare hex for anything else
// (e.g. a Vietnamese or emoji password), which is indistinguishable from a
// password that genuinely is a hex string. -g marks the two cases apart —
// see parsePasswordLine.
func get(service, account string) (string, error) {
	cmd := exec.Command(securityBin, "find-generic-password", "-s", service, "-a", account, "-g")
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard // item attributes, not needed
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("secret not found in Keychain (service=%s account=%s): %w", service, account, err)
	}
	for _, line := range strings.Split(stderr.String(), "\n") {
		if v, ok, err := parsePasswordLine(line); ok {
			return v, err
		}
	}
	return "", fmt.Errorf("unexpected `security find-generic-password -g` output for service=%s account=%s", service, account)
}

// parsePasswordLine decodes the `password: ...` line printed by
// `security find-generic-password -g`, which takes one of three shapes:
//
//	password:                          (empty secret)
//	password: "value"                  (printable ASCII without `"` or `\`)
//	password: 0x<HEX>  "<escaped>"     (anything else — the hex is authoritative)
func parsePasswordLine(line string) (value string, ok bool, err error) {
	const prefix = "password:"
	if !strings.HasPrefix(line, prefix) {
		return "", false, nil
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(line, prefix), " ")
	switch {
	case rest == "":
		return "", true, nil
	case strings.HasPrefix(rest, "0x"):
		hexPart, _, _ := strings.Cut(rest[2:], " ")
		b, err := hex.DecodeString(hexPart)
		if err != nil {
			return "", true, fmt.Errorf("decode Keychain password hex: %w", err)
		}
		return string(b), true, nil
	case len(rest) >= 2 && rest[0] == '"' && rest[len(rest)-1] == '"':
		return rest[1 : len(rest)-1], true, nil
	default:
		return "", true, fmt.Errorf("unrecognized Keychain password line format")
	}
}

func delete_(service, account string) error {
	cmd := exec.Command(securityBin, "delete-generic-password", "-s", service, "-a", account)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Not found is not an error for a delete.
		if strings.Contains(stderr.String(), "could not be found") {
			return nil
		}
		return fmt.Errorf("delete secret from Keychain: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
