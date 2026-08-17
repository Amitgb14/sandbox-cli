package sandbox

import (
	"os"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// A run configured for a gateway with no key is refused before it starts.
//
// sandbox-cli supplies none, so the alternatives are both silent and both wrong:
// reach the gateway unauthenticated, or fall through to the vendor on the
// agent's own credential — spending the wrong account against the wrong endpoint
// and reporting nothing.
func TestAGatewayWithNoKeyIsRefused(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway = &config.GatewaySpec{Agents: []string{"codex"}}
	// Nothing exported, nothing brokered.
	// Setenv restores the old value on cleanup; Unsetenv after it is what makes
	// the variable genuinely absent for this test.
	t.Setenv("OPENAI_API_KEY", "")
	os.Unsetenv("OPENAI_API_KEY")

	_, err := BuildSpec(cfg, Options{Project: t.TempDir(), Agent: "codex"})
	if err == nil {
		t.Fatal("a gateway run with no key was accepted")
	}
	if !strings.Contains(err.Error(), "never supplies this key") {
		t.Errorf("the refusal does not say whose key this is: %v", err)
	}
	if !strings.Contains(err.Error(), "secrets:") {
		t.Errorf("the refusal does not name the other way to provide it: %v", err)
	}
}

// With the user's own key exported, the run is built — and what reaches the
// container is the base URL as a setting and the key *by name*, never a value
// this package chose.
func TestAGatewayForwardsTheUsersKeyByName(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway = &config.GatewaySpec{Agents: []string{"codex"}}
	t.Setenv("OPENAI_API_KEY", "the-users-own-key")

	spec, err := BuildSpec(cfg, Options{Project: t.TempDir(), Agent: "codex"})
	if err != nil {
		t.Fatalf("BuildSpec: %v", err)
	}

	if got := spec.Env["OPENAI_BASE_URL"]; got != "https://openrouter.ai/api/v1" {
		t.Errorf("OPENAI_BASE_URL = %q, want the gateway's endpoint", got)
	}
	for k, v := range spec.Env {
		if strings.Contains(v, "the-users-own-key") {
			t.Errorf("the key's value is in the rendered spec, under %s", k)
		}
	}
	// Forwarded by name, which is how every other credential crosses.
	var forwarded bool
	for _, n := range spec.EnvNames {
		if n == "OPENAI_API_KEY" {
			forwarded = true
		}
	}
	if !forwarded {
		t.Errorf("the key was not forwarded by name: %v", spec.EnvNames)
	}
}

// An allowlist that does not permit the gateway is a run that cannot reach the
// thing it was configured to use — and it fails as a connection error from the
// agent, naming nothing.
func TestTheGatewayHostJoinsTheAllowlist(t *testing.T) {
	cfg := config.Default()
	cfg.Network.Mode = "allowlist"
	cfg.Gateway = &config.GatewaySpec{Agents: []string{"codex"}}
	t.Setenv("OPENAI_API_KEY", "the-users-own-key")

	spec, err := BuildSpec(cfg, Options{Project: t.TempDir(), Agent: "codex"})
	if err != nil {
		t.Fatalf("BuildSpec: %v", err)
	}
	allowed := spec.Env["SANDBOX_EGRESS_ALLOW"]
	if !strings.Contains(allowed, "openrouter.ai") {
		t.Errorf("the gateway host is not in the allowlist: %q", allowed)
	}
}
