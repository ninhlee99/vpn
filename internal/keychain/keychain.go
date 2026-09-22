// Package keychain stores and retrieves VPN secrets (PSKs, account
// passwords) using the macOS login Keychain via the system `security` tool.
// This is the standard, documented way for a non-sandboxed CLI to use
// Keychain without linking Security.framework through cgo — no third-party
// dependency, no secret ever written to disk outside the Keychain itself.
package keychain

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

const servicePrefix = "vpn"

// legacyServicePrefix is what this package used before the project was
// renamed from vpn-l2tp to vpn — GetPSK/GetPassword fall back to it so an
// existing install's already-stored secrets keep working after an update
// instead of suddenly reporting "not found".
const legacyServicePrefix = "vpn-l2tp"

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

func legacyPskService(profile string) string {
	return fmt.Sprintf("%s.psk.%s", legacyServicePrefix, profile)
}

func legacyPasswordService(profile, account string) string {
	return fmt.Sprintf("%s.pwd.%s.%s", legacyServicePrefix, profile, account)
}

// SetPSK stores (or overwrites) the IPsec pre-shared key for a profile.
func SetPSK(profile, psk string) error {
	return set(pskService(profile), profile, psk)
}

// GetPSK retrieves the IPsec pre-shared key for a profile. Falls back to
// (and migrates) an item stored under the pre-rename service name — see
// legacyServicePrefix.
func GetPSK(profile string) (string, error) {
	v, err := get(pskService(profile), profile)
	if err == nil {
		return v, nil
	}
	return migrateGet(pskService(profile), legacyPskService(profile), profile, err)
}

// DeletePSK removes the stored PSK for a profile, if any.
func DeletePSK(profile string) error {
	return delete_(pskService(profile), profile)
}

// SetPassword stores (or overwrites) an account's VPN login password.
func SetPassword(profile, account, password string) error {
	return set(passwordService(profile, account), account, password)
}

// GetPassword retrieves an account's VPN login password. Falls back to
// (and migrates) an item stored under the pre-rename service name — see
// legacyServicePrefix.
func GetPassword(profile, account string) (string, error) {
	v, err := get(passwordService(profile, account), account)
	if err == nil {
		return v, nil
	}
	return migrateGet(passwordService(profile, account), legacyPasswordService(profile, account), account, err)
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

func set(service, account, secret string) error {
	// -U: update in place if it already exists, instead of erroring.
	cmd := exec.Command(securityBin, "add-generic-password",
		"-U",
		"-s", service,
		"-a", account,
		"-w", secret,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("store secret in Keychain: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// migrateGet is GetPSK/GetPassword's fallback path: try the legacy
// (pre-rename) service name, and if found there, move it — store under
// newService, delete legacyService — so this only ever happens once per
// item instead of silently reading from the legacy name forever.
func migrateGet(newService, legacyService, account string, newErr error) (string, error) {
	v, err := get(legacyService, account)
	if err != nil {
		return "", newErr // neither name has it — report the original (new-name) error
	}
	if err := set(newService, account, v); err == nil {
		_ = delete_(legacyService, account)
	}
	return v, nil
}

func get(service, account string) (string, error) {
	cmd := exec.Command(securityBin, "find-generic-password", "-s", service, "-a", account, "-w")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("secret not found in Keychain (service=%s account=%s): %w", service, account, err)
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
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
