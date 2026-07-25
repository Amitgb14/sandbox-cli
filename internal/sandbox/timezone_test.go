package sandbox

import (
	"testing"
)

// pinTimezone makes the host's real zone irrelevant to a test: BuildSpec must
// render the same spec on every machine, and this is the one input that differs
// on each of them.
func pinTimezone(t *testing.T, zone string) {
	t.Helper()
	prev := hostTimezone
	hostTimezone = func() string { return zone }
	t.Cleanup(func() { hostTimezone = prev })
}

// The whole point of the feature: a git commit made inside the container should
// record the offset the user works in, not the UTC the image was built with.
func TestBuildSpecForwardsTheHostTimezone(t *testing.T) {
	pinTimezone(t, "America/Los_Angeles")
	spec, err := BuildSpec(baseCfg(), Options{Project: t.TempDir(), Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Env["TZ"] != "America/Los_Angeles" {
		t.Errorf("Env[TZ] = %q, want America/Los_Angeles", spec.Env["TZ"])
	}
}

// A host whose zone cannot be established keeps the behavior it has always had:
// UTC, and no TZ in the argv at all. An empty TZ would be a guess wearing the
// clothes of a decision.
func TestBuildSpecOmitsAnUnknownTimezone(t *testing.T) {
	pinTimezone(t, "")
	spec, err := BuildSpec(baseCfg(), Options{Project: t.TempDir(), Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := spec.Env["TZ"]; ok {
		t.Errorf("Env[TZ] = %q, want it absent", v)
	}
}

// This is a default, and every default here yields to the user saying otherwise.
func TestBuildSpecTimezoneYieldsToTheUser(t *testing.T) {
	pinTimezone(t, "America/Los_Angeles")
	spec, err := BuildSpec(baseCfg(), Options{
		Project: t.TempDir(),
		Env:     []string{"TZ=UTC"},
		Command: []string{"sh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Env["TZ"] != "UTC" {
		t.Errorf("Env[TZ] = %q, want the explicit UTC", spec.Env["TZ"])
	}
}

// Forwarding TZ by name means "use the host's value", resolved by docker at exec
// time. Setting it here as well would put a second, conflicting -e TZ in the argv.
func TestBuildSpecTimezoneYieldsToAForwardedName(t *testing.T) {
	pinTimezone(t, "America/Los_Angeles")
	t.Setenv("TZ", "Asia/Kolkata")
	spec, err := BuildSpec(baseCfg(), Options{
		Project: t.TempDir(),
		Env:     []string{"TZ"},
		Command: []string{"sh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := spec.Env["TZ"]; ok {
		t.Errorf("Env[TZ] = %q, want it left to the forwarded name", v)
	}
	if !contains(spec.EnvNames, "TZ") {
		t.Errorf("TZ not forwarded by name: %v", spec.EnvNames)
	}
}

func TestValidZoneName(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"America/Los_Angeles", true},
		{"Asia/Kolkata", true},
		{"UTC", true},
		{"Etc/GMT+7", true},
		// A POSIX TZ string, which is what a user who sets TZ by hand may have.
		{"PST8PDT", true},
		{"", false},
		{"/usr/share/zoneinfo/UTC", false},
		{"../../etc/passwd", false},
		// Nothing that could end up meaning something else in the rendered
		// `docker run` argv.
		{"America/Los Angeles", false},
		{"UTC\nFOO=bar", false},
		{"UTC=x", false},
		{"$(whoami)", false},
	} {
		if got := validZoneName(tc.in); got != tc.want {
			t.Errorf("validZoneName(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TZ set on the host is an answer already given, and beats reading the system
// files to second-guess it.
func TestResolveHostTimezonePrefersTheEnvironment(t *testing.T) {
	t.Setenv("TZ", "Asia/Kolkata")
	if got := resolveHostTimezone(); got != "Asia/Kolkata" {
		t.Errorf("resolveHostTimezone = %q, want Asia/Kolkata", got)
	}
}

// A TZ that cannot be a zone name is dropped rather than forwarded, falling
// through to the system files — the container keeping UTC is a better outcome
// than an argv carrying whatever that value was.
func TestResolveHostTimezoneRejectsNonsense(t *testing.T) {
	t.Setenv("TZ", "Not A Zone; rm -rf /")
	if got := resolveHostTimezone(); got == "Not A Zone; rm -rf /" {
		t.Error("resolveHostTimezone forwarded a value that is not a zone name")
	}
}
