package config

import (
	"reflect"
	"strings"
	"testing"
)

func gatewayConfig(agents ...string) Config {
	c := Default()
	c.Gateway = &GatewaySpec{Agents: agents}
	return c
}

// The rule the whole feature is built around: sandbox-cli names a key and never
// holds one. Nothing in a resolved gateway is a credential, and the value only
// ever arrives from the caller.
func TestAGatewayCarriesTheNameOfAKeyAndNeverAKey(t *testing.T) {
	g, err := gatewayConfig("opencode").GatewayFor("opencode")
	if err != nil || g == nil {
		t.Fatalf("GatewayFor: %v (gateway %v)", err, g)
	}
	if g.KeyEnv != "OPENROUTER_API_KEY" {
		t.Errorf("KeyEnv = %q, want the agent's documented name", g.KeyEnv)
	}

	// Every field, checked for anything that looks like a secret rather than a
	// name — the point being that there is nowhere for one to live.
	v := reflect.ValueOf(*g)
	for i := range v.NumField() {
		f, ok := v.Field(i).Interface().(string)
		if !ok {
			continue
		}
		if strings.HasPrefix(f, "sk-") || strings.HasPrefix(f, "or-") {
			t.Errorf("%s carries something shaped like a credential: %q", v.Type().Field(i).Name, f)
		}
	}

	// And the env it produces has a value only when one is handed in.
	if got := g.Env(""); len(got) != 0 {
		t.Errorf("Env(\"\") = %v, want nothing — with no key there is nothing to set", got)
	}
	if got := g.Env("the-users-key"); len(got) != 1 || got[0] != "OPENROUTER_API_KEY=the-users-key" {
		t.Errorf("Env(key) = %v, want the caller's value under the agent's name", got)
	}
}

// An agent that speaks its vendor's own protocol is refused rather than pointed
// at an OpenAI-shaped gateway. The failure is otherwise a parse error inside a
// container, minutes later, blamed on the model.
func TestAnAgentThatCannotSpeakTheShapeIsRefused(t *testing.T) {
	for _, agent := range []string{"claude", "gemini", "droid"} {
		_, err := gatewayConfig(agent).GatewayFor(agent)
		if err == nil {
			t.Errorf("%s was accepted for an OpenAI-compatible gateway", agent)
			continue
		}
		if !strings.Contains(err.Error(), "own API shape") {
			t.Errorf("%s: refusal does not say why: %v", agent, err)
		}
		// And it says what would have worked.
		if !strings.Contains(err.Error(), "opencode") {
			t.Errorf("%s: refusal does not name an agent that can: %v", agent, err)
		}
	}
}

// Only the agents named are affected: a gateway is opt-in per agent, because a
// fleet mixing one that goes through it with one that does not is the ordinary
// case rather than the exception.
func TestAGatewayAppliesOnlyToTheAgentsNamed(t *testing.T) {
	c := gatewayConfig("opencode")
	if g, err := c.GatewayFor("codex"); err != nil || g != nil {
		t.Errorf("codex picked up a gateway it was not named in: %v, %v", g, err)
	}
	if g, err := c.GatewayFor(""); err != nil || g != nil {
		t.Errorf("a run with no agent resolved a gateway: %v, %v", g, err)
	}
}

// The endpoint has to be https: the credential and every prompt cross it.
func TestAPlaintextGatewayIsRefused(t *testing.T) {
	c := gatewayConfig("codex")
	c.Gateway.BaseURL = "http://gateway.internal/v1"
	if _, err := c.GatewayFor("codex"); err == nil {
		t.Fatal("a plaintext gateway URL was accepted")
	}
}

// The probe follows the traffic. An agent talking to a gateway whose health is
// measured at the vendor gets skipped exactly when the vendor is down — the
// outage a gateway exists to survive — so routing would fail away from the one
// agent still working.
func TestProbingFollowsTheGateway(t *testing.T) {
	c := gatewayConfig("codex")
	hosts := c.ProbeHosts()
	if hosts["codex"] != "openrouter.ai" {
		t.Errorf("codex probes %q, want the gateway it actually talks to", hosts["codex"])
	}

	// An explicit providers: entry still wins — it is the more specific statement,
	// and a gateway behind somebody's own proxy is a real setup.
	c.Providers = map[string]string{"codex": "proxy.internal"}
	if got := c.ProbeHosts()["codex"]; got != "proxy.internal" {
		t.Errorf("codex probes %q, want the host the user named", got)
	}
}

// With no gateway configured, nothing about probing changes.
func TestProbeHostsIsUnchangedWithoutAGateway(t *testing.T) {
	c := Default()
	c.Providers = map[string]string{"opencode": "api.groq.com"}
	if got := c.ProbeHosts(); !reflect.DeepEqual(got, c.Providers) {
		t.Errorf("ProbeHosts() = %v, want the providers map untouched", got)
	}
}
