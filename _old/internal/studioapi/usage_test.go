package studioapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The two fields a client needs to tell "there are no numbers" from "there are
// numbers nobody can refresh" apart. Both are asserted on the wire rather than
// on the struct, because the JSON names are the contract the UI reads.
func TestUsageAlwaysCarriesWindowsAndCanRefresh(t *testing.T) {
	s, _ := newTestServer(t)
	rec := doRequest(t, s.Handler(), http.MethodGet, "/v1/usage", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	// windows is a list even when empty: a client must be able to say "no
	// windows" without distinguishing that from a missing key.
	if !strings.Contains(body, `"windows":`) {
		t.Errorf("windows key is missing:\n%s", body)
	}
	// canRefresh is never omitted. Its absence would read as false, which is a
	// claim about this machine rather than the silence it actually is.
	if !strings.Contains(body, `"canRefresh":`) {
		t.Errorf("canRefresh key is missing; a client cannot then tell whether to offer one:\n%s", body)
	}

	var got UsageSnapshot
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if got.Windows == nil {
		t.Error("windows decoded to nil rather than an empty list")
	}
}

// A window's active flag is a tri-state on the wire: true, false, or null when
// the agent reported nothing. Collapsing null to false would let the UI print
// "idle" about an allowance nobody said anything about.
func TestUsageWindowActiveIsNullable(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   UsageWindow
		want string
	}{
		{"unreported", UsageWindow{Kind: "five_hour"}, `"active":null`},
		{"in force", UsageWindow{Kind: "five_hour", Active: boolp(true)}, `"active":true`},
		{"idle", UsageWindow{Kind: "seven_day", Active: boolp(false)}, `"active":false`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), tc.want) {
				t.Errorf("marshalled %s, want it to contain %s", b, tc.want)
			}
		})
	}
}

func boolp(b bool) *bool { return &b }

// The two failures are different kinds and must not share a status. "The agent
// is not installed here" is a permanent property of this deployment — the
// ordinary case under `docker compose --profile api`, where the daemon is a
// container with no claude binary while the cache it serves is perfectly
// readable. Answering that with 502 made it look like a bad minute upstream,
// and filled the API log with what read as errors on a setup that was working
// as designed.
func TestRefreshSeparatesCannotFromFailed(t *testing.T) {
	s, _ := newTestServer(t)

	orig := usageRefreshable
	t.Cleanup(func() { usageRefreshable = orig })
	usageRefreshable = func() bool { return false }

	rec := doRequest(t, s.Handler(), http.MethodPost, "/v1/usage/refresh", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501 — a deployment that can never refresh is not a bad gateway", rec.Code)
	}
	for _, want := range []string{"cannot refresh", "PATH"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("refusal must mention %q, got: %s", want, rec.Body.String())
		}
	}
	// And it must not have tried: the point is to answer from what is known
	// rather than by failing a subprocess.
	if strings.Contains(rec.Body.String(), "exit status") {
		t.Errorf("it attempted the refresh anyway: %s", rec.Body.String())
	}
}
