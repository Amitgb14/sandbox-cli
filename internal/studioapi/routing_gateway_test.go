package studioapi

import (
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// A gateway agent is reported as going *through* something, not as talking to a
// vendor that happens to be called openrouter.ai.
//
// The distinction is the whole reason the field exists: a screen built from
// `host` alone draws the gateway as the agent's provider, which says the opposite
// of what is true — that several agents share one credential, one bill and one
// point of failure that no chain can route around.
func TestAGatewayAgentReportsWhatItGoesThrough(t *testing.T) {
	s, _ := newTestServer(t)
	s.Session.Cfg.Gateway = &config.GatewaySpec{Agents: []string{"opencode"}}

	hosts := gatewayHosts(s)
	if hosts["opencode"] != "openrouter.ai" {
		t.Fatalf("gatewayHosts = %v, want opencode through openrouter.ai", hosts)
	}

	// And the agents that were not named are untouched — a gateway is opt-in per
	// agent, so a fleet can mix one that uses it with one that does not.
	if _, through := hosts["claude"]; through {
		t.Error("claude was reported as going through a gateway it was never named in")
	}
}

// With no gateway configured, nothing is reported and nothing changes.
func TestNoGatewayReportsNothing(t *testing.T) {
	s, _ := newTestServer(t)
	if got := gatewayHosts(s); len(got) != 0 {
		t.Errorf("gatewayHosts = %v, want nothing", got)
	}
}
