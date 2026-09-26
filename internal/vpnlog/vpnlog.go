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
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const Path = "/var/log/vpn.log"

// RotatedPath keeps the previous log once Path outgrows maxSize. Rotation
// happens when a connect starts (Init, the only moment this process is
// root and may rename files in /var/log): a single session is not capped
// mid-flight by rotation (see capWriter for the in-session bound), but the
// log can no longer grow across sessions. Milestones (Info) and errors are
// always written; verbose adds the per-packet Debug lines.
const RotatedPath = Path + ".1"

const maxSize = 8 << 20 // 8 MiB

// sessionCap bounds one running session's log growth. The data plane runs
// unprivileged and so cannot rename files in /var/log, but it can still
// truncate the fd it already holds — so once the cap is hit the file is
// emptied in place and a marker line records that.
const sessionCap = 16 << 20 // 16 MiB

// logger and verbose are read from every goroutine of a connection (the data
// plane, watchdog, rekey, IKE reader ...) while Init may run again on a
// reconnect, so they are atomics rather than plain globals.
var logger atomic.Pointer[log.Logger]
var verbose atomic.Bool
var logFile *os.File

// redactedKeys never get their value written, even if a caller passes them
// by mistake — defense in depth on top of callers simply not passing
// secrets in the first place.
var redactedKeys = map[string]bool{
	"psk": true, "password": true, "secret": true, "key": true, "iv": true,
}

// Init opens (creating if needed) the log file at 0600 and enables verbose
// (debug-level) output when v is true.
func Init(v bool) error {
	verbose.Store(v)
	if err := os.MkdirAll(filepath.Dir(Path), 0o755); err != nil {
		return err
	}
	rotate(Path, RotatedPath, maxSize)
	f, err := os.OpenFile(Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	// Init runs once per (re)connect attempt of a long-lived daemon: release
	// the previous attempt's fd instead of leaking one per attempt.
	if logFile != nil {
		_ = logFile.Close()
	}
	logFile = f
	var w io.Writer = &capWriter{f: f, limit: sessionCap}
	if v {
		w = io.MultiWriter(w, os.Stderr)
	}
	logger.Store(log.New(w, "", log.LstdFlags))
	return nil
}

// capWriter empties the log file in place once this session has written
// limit bytes, so a days-long verbose session cannot fill the disk.
type capWriter struct {
	f       *os.File
	limit   int64
	written int64
}

func (c *capWriter) Write(p []byte) (int, error) {
	if c.written+int64(len(p)) > c.limit {
		if err := c.f.Truncate(0); err == nil {
			c.written = 0
			_, _ = c.f.WriteString(time.Now().Format("2006/01/02 15:04:05") + " [LOG] log file reached its size cap and was truncated\n")
		}
	}
	n, err := c.f.Write(p)
	c.written += int64(n)
	return n, err
}

// rotate moves path to rotated once it exceeds limit. Best effort: a
// failure just means this run appends to the existing file.
func rotate(path, rotated string, limit int64) {
	if fi, err := os.Stat(path); err == nil && fi.Size() > limit {
		_ = os.Rename(path, rotated)
	}
}

// current returns the logger, falling back to stderr when Init never ran.
func current() *log.Logger {
	if l := logger.Load(); l != nil {
		return l
	}
	logger.CompareAndSwap(nil, log.New(os.Stderr, "", log.LstdFlags))
	return logger.Load()
}

// Fields is a structured log payload; values under a redacted key are
// replaced with "[REDACTED]" before formatting.
type Fields map[string]any

// Keys are sorted so the same event always renders identically — map
// iteration order is random, which made log lines hard to compare or grep.
func (f Fields) String() string {
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := f[k]
		if redactedKeys[k] {
			v = "[REDACTED]"
		}
		fmt.Fprintf(&b, " %s=%v", k, v)
	}
	return b.String()
}

// Info logs a status milestone (e.g. "IKE Phase 1 established"). Always
// written: milestones are low-volume, and without them a dropped session
// leaves nothing to diagnose it from. Only per-packet detail (Debug) is
// gated on verbose. Silent until Init, so non-connect commands print nothing.
func Info(stage, msg string, f Fields) {
	l := logger.Load()
	if l == nil {
		return // no Init: a plain CLI command (diagnose, probe), not a connect — stay quiet as before
	}
	l.Printf("[%s] %s%s", stage, msg, f.String())
}

func Warn(stage, msg string, f Fields) {
	current().Printf("[%s] WARN %s%s", stage, msg, f.String())
}

func Error(stage, msg string, f Fields) {
	current().Printf("[%s] ERROR %s%s", stage, msg, f.String())
}

func Debug(stage, msg string, f Fields) {
	if !verbose.Load() {
		return
	}
	current().Printf("[%s] debug %s%s", stage, msg, f.String())
}

// Timed logs how long a named step took — useful for spotting retransmit
// storms/timeouts in IKE/L2TP without instrumenting every call site by hand.
func Timed(stage, msg string, start time.Time) {
	Info(stage, msg, Fields{"elapsed_ms": time.Since(start).Milliseconds()})
}
