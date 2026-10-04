package routing

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

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
		{529, false, "a provider's overloaded status is exactly the outage this exists for"},
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

// The probe asks whether a provider answers, and nothing more: no credential,
// even with the agent's key in the environment, because a probe is made from
// the host before any sandbox exists and a key sent with it would be a key
// sent somewhere the user did not run an agent.
func TestProbeCarriesNoCredential(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-probe-test")
	var got *http.Request
	restore := stubProbe(func(r *http.Request) (*http.Response, error) {
		got = r
		return &http.Response{StatusCode: 401, Body: http.NoBody}, nil
	})
	defer restore()
	Probe(context.Background(), "claude", nil)
	if got == nil {
		t.Fatal("no probe was made")
	}
	if got.Method != http.MethodHead || got.URL.Path != "/" || got.Body != nil && got.Body != http.NoBody {
		t.Errorf("probe %s %s with a body", got.Method, got.URL)
	}
	for k, v := range got.Header {
		if strings.Contains(strings.Join(v, " "), "sk-probe-test") || strings.EqualFold(k, "Authorization") ||
			strings.EqualFold(k, "Cookie") || strings.Contains(strings.ToLower(k), "api-key") {
			t.Errorf("the probe carried %s", k)
		}
	}
}
