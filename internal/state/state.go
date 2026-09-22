// Package state persists the running tunnel's status to disk so `status`,
// `disconnect`, and `repair` can inspect or act on a connection started by a
// different process invocation (connect runs in the foreground/daemonized;
// these commands are separate CLI invocations).
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Phase mirrors the engine's coarse connection lifecycle, independent of the
// fine-grained IKE/PPP sub-state-machines.
type Phase string

const (
	PhaseDisconnected Phase = "DISCONNECTED"
	PhaseConnecting   Phase = "CONNECTING"
	PhaseConnected    Phase = "CONNECTED"
	PhaseFailed       Phase = "FAILED"
)

// State is the on-disk snapshot of the running (or last) connection.
type State struct {
	Phase       Phase     `json:"phase"`
	Profile     string    `json:"profile,omitempty"`
	Account     string    `json:"account,omitempty"`
	Server      string    `json:"server,omitempty"`
	PID         int       `json:"pid,omitempty"`
	TunDevice   string    `json:"tun_device,omitempty"`
	LocalIP     string    `json:"local_ip,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
	FailStage   string    `json:"fail_stage,omitempty"`
	FailDetail  string    `json:"fail_detail,omitempty"`
	SavedRoutes bool      `json:"saved_routes"` // true once original routing/DNS captured for repair/restore

	// DNS snapshot, captured before Apply so disconnect/repair can restore
	// it even if that's a different process invocation than the one that
	// connected (e.g. after a crash — see dnsmgr.Snapshot). DNSApplied
	// distinguishes "no DNS servers were pushed" (nothing to restore) from
	// "the original config was itself empty/DHCP" (restore to Empty).
	DNSService string   `json:"dns_service,omitempty"`
	DNSServers []string `json:"dns_servers,omitempty"`
	DNSApplied bool     `json:"dns_applied,omitempty"`
}

// Dir is where the state file lives — exported so `uninstall` can remove it
// without needing its own copy of the path.
const Dir = "/var/run/vpn"

func runDir() (string, error) {
	// /var/run requires root, which connect/disconnect/repair already need
	// (route and utun changes are root-only on macOS), so the state file
	// lives there rather than under the user's home.
	if err := os.MkdirAll(Dir, 0o755); err != nil {
		return "", err
	}
	return Dir, nil
}

func path() (string, error) {
	dir, err := runDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "state.json"), nil
}

// Load reads the current state, returning a DISCONNECTED state if no file
// exists (nothing has ever connected).
func Load() (*State, error) {
	p, err := path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return &State{Phase: PhaseDisconnected}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Save persists s atomically.
func (s *State) Save() error {
	p, err := path()
	if err != nil {
		return err
	}
	s.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Clear resets to DISCONNECTED, used once disconnect/repair has actually
// torn everything down.
func Clear() error {
	s := &State{Phase: PhaseDisconnected}
	return s.Save()
}
