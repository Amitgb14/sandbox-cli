package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
)

// Resolving "this agent talks to a gateway" into the four facts a run needs.
//
// The four have to agree or the run is worse than not configured: the base URL
// decides where the calls go, the key variable decides what pays for them, the
// host decides what routing *probes* — measuring the vendor's health while
// talking to a gateway is a chain that skips a working agent — and the egress
// allowlist decides whether any of it is reachable at all. Working them out
// separately is how three of them end up right.

// GatewayFor resolves the gateway settings for one agent, or reports that it has
// none.
//
// The refusals are the substance. An agent that cannot speak the gateway's
// protocol is refused rather than pointed at it, because that failure lands
// inside the container as a parse error minutes later. A key variable that is
// not set anywhere is refused too, and that one is the rule this feature is
// built around: **sandbox-cli supplies no key**, so a run configured for a
// gateway with nothing to read would fall back to the vendor's own credential —
// spending the wrong account, against the wrong endpoint, silently.
func (c Config) GatewayFor(agent string) (*ResolvedGateway, error) {
	if c.Gateway == nil || agent == "" {
		return nil, nil
	}
	if !containsFold(c.Gateway.Agents, agent) {
		return nil, nil
	}

	support, ok := agents.OpenRouter(agent)
	if !ok {
		return nil, fmt.Errorf(
			"%s cannot be pointed at an OpenAI-compatible gateway: it speaks its vendor's own API shape, "+
				"so a base URL alone would fail inside the container rather than here (agents that can: %s).\n"+
				"  Drop it from gateway.agents, or put a translating proxy in front and name that with providers:",
			agent, strings.Join(agents.OpenRouterAgents(), ", "))
	}

	keyEnv := c.Gateway.KeyEnv
	if keyEnv == "" {
		keyEnv = support.KeyEnv
	}
	if keyEnv == "" {
		keyEnv = "OPENROUTER_API_KEY"
	}
	if IsReservedEnv(keyEnv) {
		return nil, fmt.Errorf("gateway.key_env cannot be %s: %s", keyEnv, ReservedEnvReason())
	}

	base := strings.TrimSpace(c.Gateway.BaseURL)
	if base == "" {
		base = agents.OpenRouterBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf(
			"gateway.base_url %q is not an https URL: the credential and every prompt cross this connection, "+
				"so plaintext is not offered even for a gateway on your own network", base)
	}

	host := strings.TrimSpace(c.Gateway.Host)
	if host == "" {
		host = u.Hostname()
	}

	return &ResolvedGateway{
		Agent:      agent,
		BaseURL:    base,
		BaseURLEnv: support.BaseURLEnv,
		KeyEnv:     keyEnv,
		Host:       host,
		Verified:   support.Verified,
		Note:       support.Note,
	}, nil
}

// ResolvedGateway is what a run does about a gateway, once the config and the
// agent's own table have been reconciled.
type ResolvedGateway struct {
	Agent string
	// BaseURL is where the agent's API calls go, and BaseURLEnv the variable that
	// redirects them — empty for an agent that selects the provider itself
	// (opencode), where the key alone is the wiring.
	BaseURL    string
	BaseURLEnv string
	// KeyEnv is the *name* of the credential variable. sandbox-cli never holds
	// its value; see GatewayFor.
	KeyEnv string
	// Host is what routing probes and what the egress allowlist must permit.
	Host string
	// Verified and Note carry the agent table's honesty about this pairing.
	Verified bool
	Note     string
}

// Env is what the container is told, given the key's value.
//
// The value is passed in by the caller that resolved it — from the user's
// environment or from the secrets broker — rather than read here, so this
// package never touches a credential and there is exactly one place that does.
func (g *ResolvedGateway) Env(key string) []string {
	if g == nil {
		return nil
	}
	var out []string
	if g.BaseURLEnv != "" {
		out = append(out, g.BaseURLEnv+"="+g.BaseURL)
	}
	if key != "" {
		out = append(out, g.KeyEnv+"="+key)
	}
	return out
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(strings.TrimSpace(s), want) {
			return true
		}
	}
	return false
}

// ProbeHosts is what routing should ask about each agent: the user's own
// `providers:` overrides, with a gateway agent's host layered on top.
//
// Layered rather than merged in the caller because getting it wrong is invisible
// in the right direction: an agent talking to a gateway whose health is measured
// at the *vendor* gets skipped when the vendor is down — which is exactly the
// outage a gateway is bought to survive, so routing would fail over away from
// the one agent that still worked.
//
// An explicit `providers:` entry still wins. Someone who has named a host for an
// agent has said something more specific than "it goes through the gateway",
// and the commonest reason to do both is a gateway behind a proxy of their own.
func (c Config) ProbeHosts() map[string]string {
	out := map[string]string{}
	if c.Gateway == nil {
		return c.Providers
	}
	for _, agent := range c.Gateway.Agents {
		agent = strings.TrimSpace(agent)
		if agent == "" {
			continue
		}
		if g, err := c.GatewayFor(agent); err == nil && g != nil && g.Host != "" {
			out[agent] = g.Host
		}
	}
	// The user's own, last: an entry they typed outranks one this derived.
	for agent, host := range c.Providers {
		out[agent] = host
	}
	return out
}
