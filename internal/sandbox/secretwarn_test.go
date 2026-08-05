package sandbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/creds"
)

func captureSecretWarnings(t *testing.T) *[]string {
	t.Helper()
	var got []string
	prev := warnedSecret
	warnedSecret = func(format string, args ...any) {
		got = append(got, fmt.Sprintf(format, args...))
	}
	t.Cleanup(func() { warnedSecret = prev })
	return &got
}

func TestWarnLongLivedSecrets(t *testing.T) {
	got := captureSecretWarnings(t)

	warnLongLivedSecrets([]creds.EnvVar{
		{Name: "GITHUB_TOKEN", Value: "ghp_" + strings.Repeat("a", 36)},
		{Name: "SESSION_TOKEN", Value: "ghs_" + strings.Repeat("b", 36)},
		{Name: "OPAQUE", Value: "b7c1f0e2d3a4"},
	}, time.Unix(1_700_000_000, 0))

	if len(*got) != 1 {
		t.Fatalf("warnings = %d %v, want exactly one (the long-lived PAT)", len(*got), *got)
	}
	if !strings.Contains((*got)[0], "GITHUB_TOKEN") {
		t.Errorf("warning does not name the secret: %q", (*got)[0])
	}
}

// The warning goes to a stream somebody may be logging, and the whole point of
// the broker is that a value is never written down. Two secrets whose *shapes*
// are recognized, so both take the branch that prints.
func TestWarningNeverContainsTheValue(t *testing.T) {
	got := captureSecretWarnings(t)

	pat := "ghp_" + strings.Repeat("s", 36)
	key := "sk-ant-api03-" + strings.Repeat("t", 20)
	warnLongLivedSecrets([]creds.EnvVar{
		{Name: "GITHUB_TOKEN", Value: pat},
		{Name: "ANTHROPIC_API_KEY", Value: key},
	}, time.Unix(1_700_000_000, 0))

	if len(*got) != 2 {
		t.Fatalf("warnings = %d, want 2", len(*got))
	}
	for _, line := range *got {
		for _, secret := range []string{pat, key} {
			for i := 0; i+8 <= len(secret); i++ {
				if strings.Contains(line, secret[i:i+8]) {
					t.Fatalf("warning %q contains a fragment of a secret value", line)
				}
			}
		}
	}
}

// A run whose secrets are all short-lived or unrecognized says nothing at all —
// a warning on every run is one nobody reads by the third.
func TestNoWarningWithoutALongLivedSecret(t *testing.T) {
	got := captureSecretWarnings(t)

	warnLongLivedSecrets([]creds.EnvVar{
		{Name: "SESSION_TOKEN", Value: "ghs_" + strings.Repeat("c", 36)},
		{Name: "OPAQUE", Value: "b7c1f0e2d3a4"},
	}, time.Unix(1_700_000_000, 0))

	if len(*got) != 0 {
		t.Errorf("warnings = %v, want none", *got)
	}
}

// The wiring: a real resolution through forwardedValues warns, so the check
// cannot be left connected to nothing. --dry-run is the other half of this and
// is pinned in spec_test.go: Prepare never resolves a secret, so it never warns.
func TestForwardedValuesWarnsOnALongLivedSecret(t *testing.T) {
	got := captureSecretWarnings(t)

	pat := "ghp_" + strings.Repeat("w", 36)
	t.Setenv("SANDBOX_TEST_LONG_LIVED", pat)
	vals, err := forwardedValues(config.Default(), Options{
		Secrets: []string{"GITHUB_TOKEN=env:SANDBOX_TEST_LONG_LIVED"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if vals["GITHUB_TOKEN"] != pat {
		t.Fatalf("the secret did not reach the container: %q", vals["GITHUB_TOKEN"])
	}
	if len(*got) != 1 || !strings.Contains((*got)[0], "GITHUB_TOKEN") {
		t.Errorf("warnings = %v, want one naming GITHUB_TOKEN", *got)
	}
}
