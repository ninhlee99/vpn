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
