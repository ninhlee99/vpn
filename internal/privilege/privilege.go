// Package privilege implements the standard setuid-root safety pattern:
// drop to the real (invoking) user by default, and only regain root for
// the specific, minimal operations that actually need it (opening a utun
// device, binding UDP/500, running route/ifconfig/networksetup) — never
// leaving the process privileged longer than one such operation needs.
//
// This is what makes it safe to install this binary setuid-root so `vpn
// connect` etc. don't need `sudo` on every invocation: without dropping by
// default, every subcommand (including ones that just read local config,
// like `profile add`) would run fully as root for its whole lifetime,
// which is unnecessary privilege the process doesn't need and shouldn't
// hold. macOS credentials are process-wide (unlike Linux, where a raw
// setuid(2) call only affects the calling OS thread — see
// https://github.com/golang/go/issues/1435 — that bug does not apply
// here), so a plain syscall.Seteuid from any goroutine correctly changes
// privilege for the whole process on Darwin.
package privilege

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"syscall"
)

// realUID is the actual invoking user, captured once at process start
// before Drop ever runs — this is what Elevate restores to afterward.
var realUID = os.Getuid()

// CheckOwner enforces that only the user who installed this binary can run
// it at all. allowedUID is baked in at build time by install.sh (via
// -ldflags "-X main.allowedUID=$(id -u)") — empty means "not installed via
// install.sh" (a plain dev build), which skips the check entirely. This
// exists because installing setuid-root makes the binary executable (and,
// without this check, root-capable) for *every* local account on the
// machine, not just the person who ran install.sh — call this as the very
// first thing in main(), before Drop or any subcommand dispatch, so a
// different user can't invoke this binary at all, privileged or not.
func CheckOwner(allowedUID string) error {
	if allowedUID == "" {
		return nil
	}
	want, err := strconv.Atoi(allowedUID)
	if err != nil {
		return fmt.Errorf("internal error: invalid baked-in owner uid %q", allowedUID)
	}
	if realUID != want {
		return fmt.Errorf("this vpn binary was installed by a different user (uid %d) — only that user can run it; ask them to run it, or reinstall it yourself with ./install.sh", want)
	}
	return nil
}

// mu serializes Elevate calls: this process is single-purpose (one
// connect/disconnect/repair per invocation), so there's no legitimate case
// for two goroutines needing root at once, and serializing avoids a window
// where one goroutine's Elevate...defer-drop races another's.
var mu sync.Mutex

// Drop lowers the effective UID to the real invoking user. Call this once,
// as the very first thing in main(), before any other code runs. If the
// binary isn't setuid-root (a plain `sudo vpn ...` invocation, or a
// non-root build run without install.sh's setuid step), this is a no-op:
// realUID is already 0 or already equals the effective UID.
func Drop() {
	if os.Geteuid() == 0 && realUID != 0 {
		_ = syscall.Seteuid(realUID)
	}
}

// Elevate runs fn with the effective UID raised to root, then always drops
// back to the real user before returning — even if fn panics or errors.
// Callers must keep fn's body to exactly the operations that need root;
// nothing else should run inside it.
func Elevate(fn func() error) error {
	mu.Lock()
	defer mu.Unlock()

	if os.Getuid() == 0 {
		// Invoked directly as root (e.g. a root login shell, not via
		// setuid) — already privileged, nothing to raise or restore.
		return fn()
	}
	if err := syscall.Seteuid(0); err != nil {
		return fmt.Errorf("this operation needs root — install with the setuid step (see install.sh) or run with sudo: %w", err)
	}
	defer func() { _ = syscall.Seteuid(realUID) }()
	return fn()
}
