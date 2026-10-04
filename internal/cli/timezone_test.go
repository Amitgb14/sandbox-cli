package cli

import (
	"github.com/Amitgb14/sandbox-cli/internal/api"

	"testing"
)

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

// Every run carries the client's zone, so a commit made in a sandbox has the
// offset of the person who asked for it; one the user set wins, and a zone
// that cannot be established sends nothing rather than a guess.
func TestRunsCarryTheClientsZone(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	defer func(f func() string) { hostTimezone = f }(hostTimezone)
	apply := func(env map[string]string) map[string]string {
		req := api.CreateSandboxRequest{Env: env}
		if err := applyConfig(&runFlags{}, t.TempDir(), &req, api.Capabilities{}, ""); err != nil {
			t.Fatal(err)
		}
		return req.Env
	}
	hostTimezone = func() string { return "Asia/Kolkata" }
	if got := apply(nil)["TZ"]; got != "Asia/Kolkata" {
		t.Errorf("TZ = %q", got)
	}
	if got := apply(map[string]string{"TZ": "UTC"})["TZ"]; got != "UTC" {
		t.Errorf("a TZ from --env was replaced: %q", got)
	}
	hostTimezone = func() string { return "" }
	if _, set := apply(nil)["TZ"]; set {
		t.Error("an unknown zone was sent")
	}
}
