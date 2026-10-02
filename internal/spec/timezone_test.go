package spec

import (
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
