// Package config manages vpn's non-secret profile/account configuration.
// Secrets (PSK, account passwords) are never stored here — see internal/keychain.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Account is one login identity that can be used against a Profile's server.
// A single VPN server commonly has several accounts (e.g. issued to different
// people, or a spare when the primary is already logged in elsewhere), so a
// Profile keeps a map of these rather than a single username/password pair.
type Account struct {
	Username string `json:"username"`
}

// Profile is one VPN server configuration, independent of which account is
// currently used to connect to it.
type Profile struct {
	Server         string              `json:"server"`
	ServerID       string              `json:"server_id,omitempty"`
	IKEProposals   []string            `json:"ike_proposals,omitempty"`
	ESPProposals   []string            `json:"esp_proposals,omitempty"`
	MTU            int                 `json:"mtu,omitempty"`
	FullTunnel     bool                `json:"full_tunnel"`
	DefaultAccount string              `json:"default_account,omitempty"`
	Accounts       map[string]*Account `json:"accounts"`
}

// Config is the on-disk, non-secret configuration for every known VPN
// profile plus which one is currently selected.
type Config struct {
	ActiveProfile string              `json:"active_profile,omitempty"`
	Profiles      map[string]*Profile `json:"profiles"`

	path string // resolved on Load/New, not persisted
}

// DefaultIKEProposals mirrors the IKE proposals accepted by the reference
// strongSwan config at ~/l2tp-proxy/entrypoint.sh: aes256-sha256-modp2048,
// aes128-sha1-modp1024, 3des-sha1-modp1024, most-preferred first.
var DefaultIKEProposals = []string{
	"aes256-sha256-modp2048",
	"aes128-sha1-modp1024",
	"3des-sha1-modp1024",
}

// DefaultESPProposals mirrors entrypoint.sh's esp= line.
var DefaultESPProposals = []string{
	"aes256-sha256",
	"aes128-sha1",
	"3des-sha1",
}

// Dir returns ~/.config/vpn, creating it with 0700 permissions if missing.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	dir := filepath.Join(home, ".config", "vpn")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create config directory: %w", err)
	}
	return dir, nil
}

func filePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads the config file, returning an empty Config if it does not exist
// yet (first run — nothing has been added via `vpn profile add`).
func Load() (*Config, error) {
	path, err := filePath()
	if err != nil {
		return nil, err
	}
	cfg := &Config{Profiles: map[string]*Profile{}, path: path}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.path = path
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]*Profile{}
	}
	return cfg, nil
}

// Save writes the config atomically with 0600 permissions — it may name a
// server address but never a secret.
func (c *Config) Save() error {
	if c.path == "" {
		p, err := filePath()
		if err != nil {
			return err
		}
		c.path = p
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(tmp, c.path); err != nil {
		return fmt.Errorf("commit config: %w", err)
	}
	return nil
}

// Profile looks up a profile by name, or the active one when name is "".
func (c *Config) Profile(name string) (string, *Profile, error) {
	if name == "" {
		name = c.ActiveProfile
	}
	if name == "" {
		return "", nil, fmt.Errorf("no VPN profile selected — add one in the TMS VPN menu bar app or pass --profile")
	}
	p, ok := c.Profiles[name]
	if !ok {
		return "", nil, fmt.Errorf("unknown VPN profile %q", name)
	}
	return name, p, nil
}

// Account looks up an account on a profile by name, or the profile's default
// when name is "".
func (p *Profile) Account(name string) (string, *Account, error) {
	if name == "" {
		name = p.DefaultAccount
	}
	if name == "" {
		return "", nil, fmt.Errorf("no account selected for this profile")
	}
	a, ok := p.Accounts[name]
	if !ok {
		return "", nil, fmt.Errorf("unknown account %q", name)
	}
	return name, a, nil
}

// AddProfile creates or replaces a profile definition. It fills in the
// compatibility-reference IKE/ESP proposals and MTU when the caller leaves
// them unset, rather than inventing different defaults per call site.
//
// Re-adding an existing name updates it in place: the accounts, default
// account and any hand-edited proposals of the existing profile carry over
// unless p sets them. Replacing it wholesale used to drop every account
// from the config while their passwords stayed orphaned in Keychain.
// Reports whether the profile already existed.
func (c *Config) AddProfile(name string, p *Profile) (updated bool) {
	if old, ok := c.Profiles[name]; ok {
		updated = true
		if len(p.Accounts) == 0 {
			p.Accounts = old.Accounts
		}
		if p.DefaultAccount == "" {
			p.DefaultAccount = old.DefaultAccount
		}
		if len(p.IKEProposals) == 0 {
			p.IKEProposals = old.IKEProposals
		}
		if len(p.ESPProposals) == 0 {
			p.ESPProposals = old.ESPProposals
		}
	}
	if len(p.IKEProposals) == 0 {
		p.IKEProposals = append([]string(nil), DefaultIKEProposals...)
	}
	if len(p.ESPProposals) == 0 {
		p.ESPProposals = append([]string(nil), DefaultESPProposals...)
	}
	if p.MTU == 0 {
		p.MTU = 1400
	}
	if p.Accounts == nil {
		p.Accounts = map[string]*Account{}
	}
	if c.Profiles == nil {
		c.Profiles = map[string]*Profile{}
	}
	c.Profiles[name] = p
	if c.ActiveProfile == "" {
		c.ActiveProfile = name
	}
	return updated
}

// ProfileNames returns every profile name, sorted.
func (c *Config) ProfileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
