package sandbox

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/creds"
)

func captureSecretWarnings(t *testing.T) *[]string {
	t.Helper()
	var got []string
	var mu sync.Mutex
	prev := warnedSecret
	// The dedupe is process-wide, so a test that did not clear it would pass or
	// fail depending on which test ran first.
	warnedMu.Lock()
	warnedNames = map[string]bool{}
	warnedMu.Unlock()
	warnedSecret = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
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

// A fleet resolves the same `secrets:` block once per task, so the warning used
// to repeat once per task — a twenty-task fleet said the same sentence twenty
// times, which is how a warning becomes wallpaper. This is the regression test
// for that: the second and later resolutions of the same secret say nothing.
func TestALongLivedSecretIsWarnedAboutOnlyOnce(t *testing.T) {
	got := captureSecretWarnings(t)

	pat := "ghp_" + strings.Repeat("a", 36)
	for i := 0; i < 20; i++ {
		warnLongLivedSecrets([]creds.EnvVar{
			{Name: "GITHUB_TOKEN", Value: pat},
		}, time.Unix(1_700_000_000, 0))
	}

	if len(*got) != 1 {
		t.Errorf("warnings = %d, want exactly 1 across 20 launches", len(*got))
	}
}

// Keyed by name rather than by value: a broker that mints a fresh long-lived
// token per task must not defeat the dedupe, since the advice is identical
// whichever token came back.
func TestDedupeIsByNameNotByValue(t *testing.T) {
	got := captureSecretWarnings(t)

	for i := 0; i < 5; i++ {
		warnLongLivedSecrets([]creds.EnvVar{
			{Name: "GITHUB_TOKEN", Value: "ghp_" + strings.Repeat(string(rune('a'+i)), 36)},
		}, time.Unix(1_700_000_000, 0))
	}

	if len(*got) != 1 {
		t.Errorf("warnings = %d, want 1 — a fresh value per launch must not re-warn", len(*got))
	}
}

// Two secrets are two different pieces of advice and both get said.
func TestDifferentSecretsEachWarnOnce(t *testing.T) {
	got := captureSecretWarnings(t)

	for i := 0; i < 3; i++ {
		warnLongLivedSecrets([]creds.EnvVar{
			{Name: "GITHUB_TOKEN", Value: "ghp_" + strings.Repeat("a", 36)},
			{Name: "SLACK_TOKEN", Value: "xoxb-" + strings.Repeat("b", 30)},
		}, time.Unix(1_700_000_000, 0))
	}

	if len(*got) != 2 {
		t.Fatalf("warnings = %d (%v), want 2", len(*got), *got)
	}
}

// studioapi calls Session.Start from an HTTP handler, so two concurrent POSTs to
// /runs share this state. Run with -race; without the mutex this is a data race
// on the map, not merely a duplicated line.
func TestConcurrentRunsDoNotRaceOnTheDedupe(t *testing.T) {
	got := captureSecretWarnings(t)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			warnLongLivedSecrets([]creds.EnvVar{
				{Name: "GITHUB_TOKEN", Value: "ghp_" + strings.Repeat("a", 36)},
			}, time.Unix(1_700_000_000, 0))
		}()
	}
	wg.Wait()

	if len(*got) != 1 {
		t.Errorf("warnings = %d, want 1 under concurrency", len(*got))
	}
}
