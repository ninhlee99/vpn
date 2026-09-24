package config

import "testing"

func TestAddProfileUpdateKeepsAccounts(t *testing.T) {
	c := &Config{}
	if c.AddProfile("work", &Profile{Server: "old.example", FullTunnel: true}) {
		t.Fatal("first add reported as update")
	}
	p := c.Profiles["work"]
	p.Accounts["alice"] = &Account{Username: "alice"}
	p.DefaultAccount = "alice"
	p.ESPProposals = []string{"aes128-sha1"} // hand-edited

	if !c.AddProfile("work", &Profile{Server: "new.example", FullTunnel: false}) {
		t.Fatal("re-add not reported as update")
	}
	got := c.Profiles["work"]
	if got.Server != "new.example" || got.FullTunnel {
		t.Fatalf("new settings not applied: %+v", got)
	}
	if _, ok := got.Accounts["alice"]; !ok || got.DefaultAccount != "alice" {
		t.Fatalf("re-add dropped accounts: %+v", got)
	}
	if len(got.ESPProposals) != 1 || got.ESPProposals[0] != "aes128-sha1" {
		t.Fatalf("re-add reset hand-edited proposals: %v", got.ESPProposals)
	}
}

func TestProfileNamesSorted(t *testing.T) {
	c := &Config{}
	for _, n := range []string{"b", "c", "a"} {
		c.AddProfile(n, &Profile{Server: n})
	}
	if got := c.ProfileNames(); len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("ProfileNames = %v", got)
	}
	if c.ActiveProfile != "b" {
		t.Fatalf("active profile = %q, want the first one added", c.ActiveProfile)
	}
}

func TestGlobalMTUWinsOverProfile(t *testing.T) {
	c := &Config{}
	c.AddProfile("a", &Profile{Server: "a", MTU: 1400})
	c.AddProfile("b", &Profile{Server: "b"})
	if got := c.EffectiveMTU(c.Profiles["a"]); got != 1400 {
		t.Fatalf("no global setting: mtu = %d, want the profile's 1400", got)
	}
	if err := c.SetMTU(1280); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a", "b"} {
		if got := c.EffectiveMTU(c.Profiles[n]); got != 1280 {
			t.Fatalf("profile %s: mtu = %d, want the global 1280", n, got)
		}
	}
}

func TestSetMTURejectsOtherValues(t *testing.T) {
	c := &Config{}
	for _, n := range []int{0, 1200, 1500, -1} {
		if err := c.SetMTU(n); err == nil {
			t.Fatalf("SetMTU(%d) accepted", n)
		}
	}
	if c.MTU != 0 {
		t.Fatalf("rejected value was stored: %d", c.MTU)
	}
}

func TestReAddKeepsProfileMTU(t *testing.T) {
	c := &Config{}
	c.AddProfile("w", &Profile{Server: "s", MTU: 1280})
	c.AddProfile("w", &Profile{Server: "s2"}) // e.g. the UI's edit form, which passes no --mtu
	if got := c.Profiles["w"].MTU; got != 1280 {
		t.Fatalf("re-add reset MTU to %d, want 1280 kept", got)
	}
}
