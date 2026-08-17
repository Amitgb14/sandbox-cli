package agents

import "sort"

// Pointing an agent at OpenRouter instead of the vendor it was built for.
//
// OpenRouter is a gateway: one OpenAI-shaped endpoint in front of many
// providers, with its own key and its own failover between *models*. It is
// complementary to this tool's routing rather than a replacement — that switches
// whole agents when one stops working, this switches what a single agent talks
// to — and the two compose, which is the case this table exists for.
//
// **sandbox-cli never supplies the key, and holds no default for one.** What is
// recorded here are *names*: which variable an agent reads its base URL from,
// and which one it reads its key from. The value is the user's, arriving the way
// every other credential does — forwarded from their environment if set, or
// resolved by the broker from `secrets:`. There is no bundled account, no
// fallback key, and a run configured for OpenRouter with nothing to read fails
// closed rather than quietly reaching the vendor with the vendor's credentials.
//
// The honesty rule is the same one AutonomousArgs keeps: an agent is listed only
// when the wiring is *known*, not when it looks plausible. OpenRouter serves the
// OpenAI shape, so an agent that speaks that shape against a configurable base
// URL can be pointed at it; an agent expecting a vendor's own protocol cannot,
// whatever base URL it is given, and would fail inside the container with an
// error about JSON rather than about configuration.

// OpenRouterHost is what a run configured this way actually talks to — the probe
// target, and the domain an egress allowlist has to permit.
const OpenRouterHost = "openrouter.ai"

// OpenRouterBaseURL is the endpoint OpenRouter documents for its OpenAI-shaped
// API. A default rather than a constant in the wiring, because a caller pointing
// at a self-hosted gateway of the same shape is a real setup and this is only
// where the arrow starts.
const OpenRouterBaseURL = "https://openrouter.ai/api/v1"

// OpenRouterSupport says how one agent is pointed at a gateway, or is absent
// when it cannot be.
type OpenRouterSupport struct {
	// BaseURLEnv is the variable that redirects the agent's API calls.
	BaseURLEnv string
	// KeyEnv is the variable the agent reads its credential from. Named, never
	// valued: see the note above.
	KeyEnv string
	// Verified records whether this pairing has been *run*, or is inference from
	// the agent's documented environment. Unverified support is offered with the
	// warning attached rather than presented as a supported path — the same
	// distinction ConsolePromptArgs makes about seeding a prompt, and for the
	// same reason: the failure lands inside a container, minutes later, as a
	// message about something else.
	Verified bool
	// Note is what a caller should be told when this is used, and is required
	// for an unverified entry.
	Note string
}

// openRouterSupport is the table. Absent means "cannot be pointed at OpenRouter",
// which is a real answer rather than a gap to fill in later.
var openRouterSupport = map[string]OpenRouterSupport{
	// opencode is provider-agnostic by design and already reads
	// OPENROUTER_API_KEY — it is the one agent where this is the agent's own
	// feature rather than a redirection of it.
	"opencode": {
		KeyEnv:   "OPENROUTER_API_KEY",
		Verified: true,
		Note:     "opencode selects the provider itself; set the model to an openrouter/… id in its own config.",
	},
	// codex speaks the OpenAI shape and takes a base URL, which is exactly what
	// a gateway serves.
	"codex": {
		BaseURLEnv: "OPENAI_BASE_URL",
		KeyEnv:     "OPENAI_API_KEY",
		Verified:   false,
		Note: "codex reads OPENAI_BASE_URL and the OpenAI shape, which is what OpenRouter serves, " +
			"but this pairing has not been run end to end here. Check the first run's output rather than assuming it.",
	},
	// Deliberately absent, and the absence is the point:
	//
	//   claude  — ANTHROPIC_BASE_URL expects Anthropic's own Messages API, not the
	//             OpenAI shape. Pointing it at a gateway that serves the other
	//             protocol fails inside the container while looking like a
	//             configuration that was accepted. A translating proxy in front is
	//             a different thing, and one the user can already name with
	//             `providers:` plus ANTHROPIC_BASE_URL themselves.
	//   gemini  — same objection, its own API shape.
	//   droid   — talks to its vendor's service, not a model endpoint.
}

// OpenRouter reports how this agent is pointed at a gateway.
func OpenRouter(agent string) (OpenRouterSupport, bool) {
	s, ok := openRouterSupport[agent]
	return s, ok
}

// OpenRouterAgents lists the agents that can be pointed at one, for an error
// message that says what *would* have worked.
func OpenRouterAgents() []string {
	out := make([]string, 0, len(openRouterSupport))
	for name := range openRouterSupport {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
