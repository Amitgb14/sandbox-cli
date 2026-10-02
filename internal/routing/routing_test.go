package routing

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func boolp(b bool) *bool { return &b }

// The failover rule, which is the whole feature in four lines.
//
// It gates on the **workspace**, never on the conversation, and every unknown
// resolves to "do not retry". The two are not symmetric: a retry that should not
// have happened puts a second agent on top of the first one's edits, while a
// retry that did not happen costs a command somebody types again.
func TestShouldFailOver(t *testing.T) {
	cases := []struct {
		name string
		o    Outcome
		want bool
		why  string
	}{
		{
			name: "a run that worked is never retried",
			o:    Outcome{ExitCode: 0, WorkspaceChanged: boolp(false)},
			want: false,
		},
		{
			name: "failed having written nothing — the outage case",
			o:    Outcome{ExitCode: 1, WorkspaceChanged: boolp(false)},
			want: true,
			why:  "this is what a provider dying mid-run looks like, and the only case where retrying is safe",
		},
		{
			name: "failed having changed files is an attempt, not an outage",
			o:    Outcome{ExitCode: 1, WorkspaceChanged: boolp(true)},
			want: false,
			why:  "retrying would hand the next agent the first one's half-finished edits",
		},
		{
			name: "a verify that said no changed files, so it is not retried",
			o:    Outcome{ExitCode: 91, WorkspaceChanged: boolp(true)},
			want: false,
			why:  "the work was done and judged; a second agent re-running it is not failover",
		},
		{
			name: "unknowable workspace state is treated as work done",
			o:    Outcome{ExitCode: 1, WorkspaceChanged: nil},
			want: false,
			why:  "reading unknown as unchanged would retry a run that may have done real work",
		},
		{
			name: "killed having written nothing is still an outage-shaped failure",
			o:    Outcome{ExitCode: 137, WorkspaceChanged: boolp(false)},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := ShouldFailOver(tc.o)
			if got != tc.want {
				t.Errorf("ShouldFailOver(%+v) = %v (%s), want %v — %s", tc.o, got, reason, tc.want, tc.why)
			}
			if reason == "" {
				t.Error("no reason given; the decision is written to the audit line and shown on screen, so it has to say why")
			}
		})
	}
}

func TestResolve(t *testing.T) {
	t.Run("orders primary first and keeps the fallbacks", func(t *testing.T) {
		c, err := Resolve("claude", []string{"codex", "gemini"}, true)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(c, ","); got != "claude,codex,gemini" {
			t.Errorf("chain = %s, want claude,codex,gemini", got)
		}
		if c.Primary() != "claude" {
			t.Errorf("primary = %q", c.Primary())
		}
	})

	t.Run("a repeated agent appears once", func(t *testing.T) {
		c, err := Resolve("claude", []string{"claude", "codex"}, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(c) != 2 {
			t.Errorf("chain = %v, want claude then codex: a chain listing one agent twice probes the same outage twice while looking like it has a fallback", c)
		}
	})

	t.Run("an unknown agent is refused at resolution", func(t *testing.T) {
		// Refused here rather than at the moment the primary fails, which is
		// exactly when nobody is watching.
		if _, err := Resolve("claude", []string{"nosuchagent"}, true); err == nil {
			t.Error("an unknown agent was accepted into a chain")
		}
	})

	t.Run("an empty chain is refused", func(t *testing.T) {
		if _, err := Resolve("", nil, true); err == nil {
			t.Error("an empty chain resolved")
		}
	})

	t.Run("blanks are skipped rather than becoming an agent", func(t *testing.T) {
		c, err := Resolve("claude", []string{"", "  ", "codex"}, true)
		if err != nil || len(c) != 2 {
			t.Errorf("chain = %v, err = %v; want the two real names", c, err)
		}
	})
}

// The probe is a liveness check and must not become an auth check: a probe
// carries no credentials on purpose, so the unauthenticated answers a healthy
// provider gives are *success*.
func TestProbeClassifiesResponses(t *testing.T) {
	cases := []struct {
		status    int
		reachable bool
		why       string
	}{
		{200, true, ""},
		{401, true, "unauthenticated is what a probe with no credentials should get from a healthy endpoint"},
		{403, true, "same as 401 — the endpoint answered"},
		{404, true, "the host is serving; the path is not the question"},
		{429, true, "rate limited means healthy and over-asked; failing over would route around your own quota rather than an outage"},
		{500, false, ""},
		{503, false, ""},
		{529, false, "Anthropic's overloaded status is exactly the outage this exists for"},
	}
	for _, tc := range cases {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			restore := stubProbe(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: http.NoBody}, nil
			})
			defer restore()

			got := Probe(context.Background(), "claude", nil)
			if got.Reachable != tc.reachable {
				t.Errorf("status %d → reachable %v, want %v — %s", tc.status, got.Reachable, tc.reachable, tc.why)
			}
			if !got.Probed {
				t.Error("an agent with a provider host reported itself unprobed")
			}
			if !tc.reachable && got.Reason == "" {
				t.Error("no reason for an unreachable provider; the screen has to say why it skipped an agent")
			}
		})
	}
}

// The override is what makes a provider-agnostic agent probeable at all, and
// what makes an agent behind a proxy probed against the right thing.
func TestHostForPrefersTheUsersAnswer(t *testing.T) {
	cases := []struct {
		name      string
		agent     string
		overrides map[string]string
		want      string
	}{
		{"the descriptor's own host when nobody said otherwise", "claude", nil, "api.anthropic.com"},
		{"nothing to ask for a provider-agnostic agent", "opencode", nil, ""},
		{"an override gives opencode something to ask", "opencode",
			map[string]string{"opencode": "api.groq.com"}, "api.groq.com"},
		{"an override beats the descriptor, for anyone behind a proxy", "claude",
			map[string]string{"claude": "llm.internal"}, "llm.internal"},
		{"an empty override is a deliberate do-not-probe", "claude",
			map[string]string{"claude": ""}, ""},
		{"an unknown agent has no host rather than a guess", "nosuch", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HostFor(tc.agent, tc.overrides); got != tc.want {
				t.Errorf("HostFor(%q, %v) = %q, want %q", tc.agent, tc.overrides, got, tc.want)
			}
		})
	}
}

// An override turns an unprobeable agent into a probed one — which is the whole
// point for opencode, whose EnvAllow spans five vendors because the user picks.
func TestProbeUsesTheOverride(t *testing.T) {
	restore := stubProbe(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.groq.com" {
			t.Errorf("probed %q, want the overridden host", r.URL.Host)
		}
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})
	defer restore()

	got := Probe(context.Background(), "opencode", map[string]string{"opencode": "api.groq.com"})
	if !got.Probed || !got.Reachable {
		t.Errorf("opencode with an override = %+v, want probed and reachable", got)
	}
}

// An agent with no provider host is reported *unprobed*, never as down.
// opencode is provider-agnostic and an agent behind a proxy is not talking to
// the vendor at all — treating either as down would skip a working agent.
func TestProbeSkipsAgentsWithNoProviderHost(t *testing.T) {
	got := Probe(context.Background(), "opencode", nil)
	if !got.Reachable {
		t.Error("an unprobeable agent was reported unreachable; unknown is not down")
	}
	if got.Probed {
		t.Error("an agent with no provider host claimed to have been probed")
	}
}

func TestProbeReportsTransportFailures(t *testing.T) {
	restore := stubProbe(func(*http.Request) (*http.Response, error) {
		return nil, &net0{}
	})
	defer restore()

	got := Probe(context.Background(), "claude", nil)
	if got.Reachable {
		t.Error("a provider that could not be dialled was reported reachable")
	}
	if got.Reason == "" {
		t.Error("no reason for a transport failure")
	}
}

// --- helpers -------------------------------------------------------------

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// stubProbe swaps the package client for one that never leaves the process. A
// test that needed the network would be a test that fails on a train.
func stubProbe(f roundTripFunc) func() {
	prev := httpClient
	httpClient = &http.Client{Transport: f}
	return func() { httpClient = prev }
}

// net0 is a transport error that is not a timeout, so probeError takes its
// generic path.
type net0 struct{}

func (net0) Error() string { return "dial tcp: connection refused" }
