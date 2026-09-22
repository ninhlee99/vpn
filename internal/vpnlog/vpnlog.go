// Package vpnlog is the single logging entry point for the engine. It
// exists mainly to make "never log secrets" enforceable in one place:
// callers pass structured fields, and this package is the only thing
// allowed to write to the log file.
package vpnlog

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

const Path = "/var/log/vpn.log"

// LegacyPath is where this log lived before the project was renamed from
// vpn-l2tp to vpn — `vpn uninstall` also removes it, so a rename doesn't
// strand an orphaned log file behind.
const LegacyPath = "/var/log/vpn-l2tp.log"

var logger *log.Logger
var verbose bool

// redactedKeys never get their value written, even if a caller passes them
// by mistake — defense in depth on top of callers simply not passing
// secrets in the first place.
var redactedKeys = map[string]bool{
	"psk": true, "password": true, "secret": true, "key": true, "iv": true,
}

// Init opens (creating if needed) the log file at 0600 and enables verbose
// (debug-level) output when v is true.
func Init(v bool) error {
	verbose = v
	if err := os.MkdirAll(filepath.Dir(Path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	var w io.Writer = f
	if verbose {
		w = io.MultiWriter(f, os.Stderr)
	}
	logger = log.New(w, "", log.LstdFlags)
	return nil
}

func ensure() {
	if logger == nil {
		logger = log.New(os.Stderr, "", log.LstdFlags)
	}
}

// Fields is a structured log payload; values under a redacted key are
// replaced with "[REDACTED]" before formatting.
type Fields map[string]any

func (f Fields) String() string {
	s := ""
	for k, v := range f {
		if redactedKeys[k] {
			v = "[REDACTED]"
		}
		s += fmt.Sprintf(" %s=%v", k, v)
	}
	return s
}

// Info logs a status milestone (e.g. "IKE Phase 1 established"). Only
// written when verbose is on — normal runs stay quiet in the log file
// unless something actually goes wrong (see Error), so a long-lived
// `connect` doesn't grow the log file forever for no reason.
func Info(stage, msg string, f Fields) {
	if !verbose {
		return
	}
	ensure()
	logger.Printf("[%s] %s%s", stage, msg, f.String())
}

func Error(stage, msg string, f Fields) {
	ensure()
	logger.Printf("[%s] ERROR %s%s", stage, msg, f.String())
}

func Debug(stage, msg string, f Fields) {
	if !verbose {
		return
	}
	ensure()
	logger.Printf("[%s] debug %s%s", stage, msg, f.String())
}

// Timed logs how long a named step took — useful for spotting retransmit
// storms/timeouts in IKE/L2TP without instrumenting every call site by hand.
func Timed(stage, msg string, start time.Time) {
	Info(stage, msg, Fields{"elapsed_ms": time.Since(start).Milliseconds()})
}
