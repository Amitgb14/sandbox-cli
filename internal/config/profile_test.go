package config

import (
	"reflect"
	"strings"
	"testing"
)

// TestProdProfileSatisfiesItsOwnInvariants is the drift guard the profile
// mechanism rests on.
//
// A named profile that quietly stops delivering what it promises is worse than
// no profile, because it is trusted. profileBase sets prod's settings as a base
// layer that later layers could in principle undo, so the settings that define
// prod are asserted against a resolved configuration — and asserting them
// against the base is the floor of that.
func TestProdProfileSatisfiesItsOwnInvariants(t *testing.T) {
	if err := ValidateProfile(ProfileProd, profileBase(ProfileProd)); err != nil {
		t.Fatalf("the built-in prod profile does not satisfy prod's invariants: %v", err)
	}
}

// And the invariants have to bite: a prod profile whose settings were undone by
// a later layer must be caught, or the check is decoration.
func TestValidateProfileCatchesEachWeakening(t *testing.T) {
	weakenings := map[string]func(*Config){
		"network mode": func(c *Config) { c.Network.Mode = "default" },
		"baseline back on": func(c *Config) {
			on := true
			c.Network.Baseline = &on
		},
		"persisted auth back on": func(c *Config) {
			on := true
			c.PersistAuth = &on
		},
		"history sync back on": func(c *Config) {
			on := true
			c.Sync = &on
		},
		"seccomp no longer required": func(c *Config) { c.Security.Seccomp = "" },
		"running as root":            func(c *Config) { c.User = "root" },
		"capabilities added":         func(c *Config) { c.Security.CapAdd = []string{"SYS_ADMIN"} },
	}
	for name, weaken := range weakenings {
		t.Run(name, func(t *testing.T) {
			cfg := profileBase(ProfileProd)
			weaken(&cfg)
			if err := ValidateProfile(ProfileProd, cfg); err == nil {
				t.Error("prod accepted a configuration that no longer delivers what the profile promises")
			}
		})
	}
}

// TestDevProfileIsExactlyTheDefaults pins the constraint that made this design
// acceptable: hardening work must not hamper local development. Running with no
// profile and running --profile dev cannot drift apart.
func TestDevProfileIsExactlyTheDefaults(t *testing.T) {
	want := Default()
	got := profileBase(ProfileDev)
	if !reflect.DeepEqual(want, got) {
		t.Error("the dev profile has diverged from Default(); dev must be today's defaults, not a second copy of them")
	}
}

// TestResolveProfileLetsAProjectRaiseButNeverLower is the security property of
// the whole mechanism. If a .sandbox.yaml could select the weaker profile, a
// hostile repository would drop the run out of prod and every other control
// becomes decoration.
func TestResolveProfileLetsAProjectRaiseButNeverLower(t *testing.T) {
	cases := []struct {
		name                string
		flag, user, project string
		want                string
	}{
		{"nothing selected is dev", "", "", "", ProfileDev},
		{"user selects prod", "", ProfileProd, "", ProfileProd},
		{"a project may raise to prod", "", ProfileDev, ProfileProd, ProfileProd},
		{"a project may NOT lower to dev", "", ProfileProd, ProfileDev, ProfileProd},
		{"an unknown project value is ignored", "", ProfileProd, "wide-open", ProfileProd},
		{"the flag wins over the user's config", ProfileDev, ProfileProd, "", ProfileDev},
		{"the flag wins over a project demand", ProfileDev, "", ProfileProd, ProfileDev},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveProfile(tc.flag, tc.user, tc.project)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("ResolveProfile(%q, %q, %q) = %q, want %q", tc.flag, tc.user, tc.project, got, tc.want)
			}
		})
	}
}

// An unknown --profile is a typo, and a typo that silently ran dev when the
// operator typed "production" is the whole failure mode prod exists to avoid.
func TestResolveProfileRefusesAnUnknownFlag(t *testing.T) {
	if _, err := ResolveProfile("production", "", ""); err == nil {
		t.Error("an unknown --profile was accepted; a typo would silently run the weaker profile")
	}
}

// TestLoadAppliesTheProfileAsABaseLayer covers the ordering: the profile is the
// base, so a user's own config can still tune it — but not past the invariants.
func TestLoadAppliesTheProfileAsABaseLayer(t *testing.T) {
	t.Run("prod base applies", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		cfg, err := LoadProfile(dir, "", ProfileProd)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Profile != ProfileProd {
			t.Errorf("Profile = %q, want prod", cfg.Profile)
		}
		if cfg.Network.Mode != "allowlist" || cfg.Network.BaselineEnabled() {
			t.Errorf("prod did not apply its egress posture: mode=%q baseline=%v",
				cfg.Network.Mode, cfg.Network.BaselineEnabled())
		}
		if cfg.PersistAuthEnabled() {
			t.Error("prod left persisted auth on, so the container would carry a long-lived credential")
		}
		if cfg.Security.Seccomp != SeccompRequired {
			t.Errorf("prod did not require seccomp: %q", cfg.Security.Seccomp)
		}
	})

	t.Run("dev is unchanged from no profile at all", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		withFlag, err := LoadProfile(dir, "", ProfileDev)
		if err != nil {
			t.Fatal(err)
		}
		without, err := Load(dir, "")
		if err != nil {
			t.Fatal(err)
		}
		withFlag.Profile, without.Profile = "", ""
		if !reflect.DeepEqual(withFlag, without) {
			t.Error("--profile dev differs from running with no profile")
		}
	})
}

// A user config that undoes one of prod's settings must fail at load, not run
// weaker than asked. This is ValidateProfile wired into the real path.
func TestLoadRefusesAUserConfigThatHollowsOutProd(t *testing.T) {
	err := loadWithUserAndProject(t, "profile: prod\npersist_auth: true\nnetwork:\n  allow: [api.example.com]\n", "")
	if err == nil {
		t.Fatal("a user config that turned persisted auth back on under prod was accepted")
	}
	if !strings.Contains(err.Error(), "persist_auth") {
		t.Errorf("the refusal does not name the setting at fault: %v", err)
	}
}

// A typo in the key that selects the security posture must not read as "not
// set". `profile: prodd` used to be discarded silently and run as dev, with
// persisted auth mounted and seccomp merely warned about, while the operator
// believed they had asked for prod.
func TestResolveProfileRefusesAnUnknownNameInAConfigFile(t *testing.T) {
	if _, err := ResolveProfile("", "prodd", ""); err == nil {
		t.Error("an unknown profile in the user's config was silently downgraded to dev")
	}
	// A project may only raise, so an unrecognised value there is already inert
	// and must not be able to fail someone's run.
	if got, err := ResolveProfile("", ProfileProd, "nonsense"); err != nil || got != ProfileProd {
		t.Errorf("an unrecognised project profile broke the run: %q, %v", got, err)
	}
}

// The consolidated root check has to cover every spelling docker accepts, since
// three divergent copies previously disagreed about exactly these.
func TestIsRootUserCoversEverySpelling(t *testing.T) {
	for _, u := range []string{"root", "0", "0:0", "root:root", "0:1000", "root:staff", " root "} {
		if !IsRootUser(u) {
			t.Errorf("IsRootUser(%q) = false, want true", u)
		}
	}
	for _, u := range []string{"", "sandbox", "1000", "1000:0", "rooted", "toor"} {
		if IsRootUser(u) {
			t.Errorf("IsRootUser(%q) = true, want false", u)
		}
	}
}
