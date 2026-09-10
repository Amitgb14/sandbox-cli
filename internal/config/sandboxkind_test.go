package config

import "testing"

// The zero value is the whole compatibility story: a config that never mentions
// `sandbox` must behave exactly as it did before the field existed, which means
// following `engine` rather than hardcoding docker.
func TestSandboxKindZeroValueFollowsTheEngine(t *testing.T) {
	for engine, want := range map[string]SandboxKind{
		"":       SandboxDocker, // the engine default is docker
		"docker": SandboxDocker,
		"podman": SandboxPodman,
	} {
		if got := SandboxKind("").Resolve(engine); got != want {
			t.Errorf("engine %q: resolved to %q, want %q", engine, got, want)
		}
	}
	// An explicit kind is not overridden by the engine. The two answer different
	// questions, and this is the line where that stops being a comment.
	if got := SandboxPodman.Resolve("docker"); got != SandboxPodman {
		t.Errorf("explicit podman under engine docker: got %q", got)
	}
}

// A typo and an unimplemented kind are different mistakes, and conflating them is
// how "sandbox: nono" would end up meaning something. Both are errors; the test
// is that they are *different* errors, since one is fixed by spelling and the
// other cannot be fixed at all yet.
func TestValidateSeparatesATypoFromAnUnimplementedKind(t *testing.T) {
	base := func(kind string) Config {
		c := Default()
		c.Sandbox = SandboxKind(kind)
		return c
	}
	if err := base("dokcer").Validate(); err == nil {
		t.Error("accepted a misspelled kind")
	} else if got := err.Error(); !contains(got, "want docker, podman, bwrap or none") {
		t.Errorf("typo error does not list the kinds: %s", got)
	}
	for _, kind := range []string{"none", "bwrap"} {
		err := base(kind).Validate()
		if err == nil {
			t.Fatalf("sandbox %q was accepted; it isolates nothing yet", kind)
		}
		if got := err.Error(); !contains(got, "not implemented") {
			t.Errorf("sandbox %q: %s\nwant the unimplemented refusal, not the typo one", kind, got)
		}
	}
	// And the two that work, work — under either profile, since this is not a
	// profile question.
	for _, profile := range []string{"dev", "prod"} {
		c := base("docker")
		c.Profile = profile
		if err := c.Validate(); err != nil {
			t.Errorf("sandbox docker under %s: %v", profile, err)
		}
	}
}

// The decision this type exists to make legible: `none` is refused on **dev**,
// not only on prod. Dev is the default profile, so a dev-only no-isolation mode
// would be reachable by the ordinary path rather than an unusual one — the
// profile asymmetry is about controls that could not be applied, not about
// controls nobody wrote.
func TestNoSandboxIsRefusedOnTheDefaultProfileToo(t *testing.T) {
	// Asked of ResolveProfile rather than of Default(), because the profile is a
	// config *layer* rather than a field Default() fills in — and the claim this
	// test rests on is about which profile a run with no flags gets.
	chosen, err := ResolveProfile("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if chosen != ProfileDev {
		t.Fatalf("this test is about the default profile, which is now %q", chosen)
	}

	c := Default()
	c.Profile = chosen
	c.Sandbox = SandboxNone
	if err := c.Validate(); err == nil {
		t.Fatal("dev accepted `sandbox: none` — an agent on the host, on the profile almost every run uses")
	}
	if SandboxNone.Available() || SandboxBwrap.Available() {
		t.Error("an unimplemented kind reported itself available; nothing may treat it as a fallback")
	}
	// Known but unavailable is the shape that keeps the catalog readable: the
	// value can be written down and refused, rather than being a value an older
	// reader has never heard of.
	for _, k := range []SandboxKind{SandboxDocker, SandboxPodman, SandboxBwrap, SandboxNone} {
		if !KnownSandboxKind(k) {
			t.Errorf("%q is a declared constant and not known", k)
		}
	}
	if KnownSandboxKind("chroot") {
		t.Error("an undeclared kind reported itself known")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
