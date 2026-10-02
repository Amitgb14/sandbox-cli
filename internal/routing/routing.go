// Package routing decides which agent a run actually uses when the first choice
// is unavailable.
//
// The problem it exists for is mundane and common: a provider has an outage,
// and a run that would have worked does nothing. A chain — claude, then codex —
// turns that from a failed afternoon into a slower one.
//
// Two mechanisms, and the split is the whole design:
//
//   - **Probe before launching.** Ask the primary's provider whether it is
//     answering. Down means skip to the next agent before a container is even
//     created, so nothing is half-done and there is no ambiguity about why the
//     switch happened. This is measurement, not inference.
//
//   - **Retry after a run that did nothing.** A provider can die mid-run, which
//     no preflight can catch. So a run that exits non-zero *and left the
//     workspace unchanged* is retried with the next agent.
//
// That second rule gates on the **workspace**, not on the conversation, and the
// distinction is load-bearing rather than a detail. Turns are cheap and safe to
// redo; file changes are not — the hazard of retrying was always a second agent
// inheriting the first one's half-finished edits. Gating on turns instead would
// also make the handoff pointless: every case that failed over would be one with
// no conversation to hand over, and the case worth carrying (an agent that
// thought for twelve turns, wrote nothing, then hit an outage) would be excluded.
//
// What this package will not do is retry a run that *changed files*. That is a
// failed attempt, not an outage, and the difference matters more than catching
// every possible outage: a wrong retry destroys work, a missed one costs a
// re-run somebody asks for by hand.
package routing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
)

// Chain is an ordered list of agent names, primary first.
type Chain []string

// Primary is the agent a chain starts with, or "" for an empty chain.
func (c Chain) Primary() string {
	if len(c) == 0 {
		return ""
	}
	return c[0]
}

// String renders a chain the way it is written and read: claude → codex.
func (c Chain) String() string { return strings.Join(c, " → ") }

// Resolve turns a primary and its fallbacks into a validated chain.
//
// Three rules, each of which has already cost somebody an afternoon somewhere:
//
//   - **Every name must be a known adapter.** An unknown one is refused at
//     resolution rather than discovered at the moment the primary fails, which
//     is precisely when nobody is watching.
//   - **No duplicates**, and the first mention wins. A chain that lists claude
//     twice would probe and fail the same outage twice while looking like it had
//     a fallback.
//   - **Unattended runs need a verified headless argv.** This is the
//     internal/agents rule, applied at the point a *second* agent gets chosen:
//     an agent that stops to ask permission does not fail, it hangs — and it
//     would hang in the fallback slot, where nobody is looking at all.
func Resolve(primary string, fallbacks []string, unattended bool) (Chain, error) {
	var out Chain
	seen := map[string]bool{}
	for _, name := range append([]string{primary}, fallbacks...) {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if seen[name] {
			continue
		}
		d, ok := agents.Lookup(name)
		if !ok {
			return nil, fmt.Errorf("unknown agent %q in the routing chain (known: %s)",
				name, strings.Join(agents.Names(), ", "))
		}
		if unattended && d.AutonomousArgs == nil {
			return nil, fmt.Errorf(
				"agent %q has no verified non-interactive mode, so it cannot be routed to in an unattended run: "+
					"it would not fail, it would hang waiting for an answer nobody is there to give", name)
		}
		seen[name] = true
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("routing chain is empty: name at least one agent")
	}
	return out, nil
}

// Availability is what a probe found.
type Availability struct {
	Agent string
	// Reachable is false only when the provider was *asked* and did not answer
	// usably. An agent with no probeable host is Reachable with Probed false —
	// unknown is not "down", and treating it as down would skip a working agent.
	Reachable bool
	Probed    bool
	// Reason is a short phrase for a human: "connection refused", "503", "timed
	// out". Empty when nothing was wrong.
	Reason string
}

// probeTimeout bounds one probe. This runs in front of every launch, so it has
// to be short enough that a provider being slow does not become sandbox-cli
// being slow — and a provider that cannot answer in three seconds is not one an
// agent will have a good time with either.
const probeTimeout = 3 * time.Second

// httpClient is a var so tests can supply a transport instead of reaching the
// network. Probing is the one part of this package that talks to the world, and
// a test that needed the internet would be a test that fails on a train.
var httpClient = &http.Client{Timeout: probeTimeout}

// Probe asks whether an agent's provider is answering.
//
// It is deliberately crude, and the crudeness is the point: this is a liveness
// check, not an authentication check. Any HTTP response at all means the service
// is up and talking — including 401 and 403, which say the endpoint is healthy
// and this request was unauthenticated, which it was, because a probe carries no
// credentials on purpose. Sending one would mean picking a credential to spend
// on a question that does not need one.
//
// A 5xx *is* treated as down: 500, 502, 503 and Anthropic's 529 "overloaded" are
// exactly the outage this feature exists for. So are DNS failure, connection
// refused, and a timeout.
//
// 429 is **not** treated as down, and that is a judgement worth stating: rate
// limiting means the provider is healthy and you have asked too much of it,
// which failing over would not fix — the next agent is a different account with
// a different limit, so a chain would quietly become a way to route around your
// own quota rather than around an outage.
func Probe(ctx context.Context, agent string, overrides map[string]string) Availability {
	host := HostFor(agent, overrides)
	if host == "" {
		// Nothing to ask. Reported as unprobed rather than as reachable-because-we-
		// assumed, so a caller can say "not checked" instead of implying a check.
		return Availability{Agent: agent, Reachable: true, Probed: false}
	}

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	// HEAD on the bare host: no path, no body, no credential. Every provider here
	// answers *something* to it, and something is all this asks for.
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://"+host+"/", nil)
	if err != nil {
		return Availability{Agent: agent, Probed: true, Reachable: false, Reason: err.Error()}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return Availability{Agent: agent, Probed: true, Reachable: false, Reason: probeError(err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		return Availability{
			Agent: agent, Probed: true, Reachable: false,
			Reason: fmt.Sprintf("provider answered %d", resp.StatusCode),
		}
	}
	return Availability{Agent: agent, Probed: true, Reachable: true}
}

// HostFor is the host to probe for an agent: the user's override when they have
// stated one, else the descriptor's own, else nothing.
//
// The override wins because it is the more specific statement about *this*
// machine. `opencode` ships with no host at all — it is provider-agnostic, so
// there is nothing true to compile in — and an agent pointed at a proxy through
// ANTHROPIC_BASE_URL is not talking to the vendor whose host the descriptor
// names. Only the person running it knows which of those they are.
//
// An override of "" is a deliberate "do not probe this one", which is why the
// map is consulted before the descriptor rather than as a fallback for it.
func HostFor(agent string, overrides map[string]string) string {
	if host, ok := overrides[agent]; ok {
		return strings.TrimSpace(host)
	}
	d, ok := agents.Lookup(agent)
	if !ok {
		return ""
	}
	return d.ProviderHost
}

// probeError reduces a transport error to a phrase worth printing. The full
// error names the URL and the dialer, which is noise in a one-line reason.
func probeError(err error) string {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timed out"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no such host"):
		return "no such host"
	case strings.Contains(msg, "connection refused"):
		return "connection refused"
	case strings.Contains(msg, "certificate"):
		return "TLS certificate rejected"
	}
	return "unreachable"
}

// Outcome is what one attempt did, and it is the input to the failover rule.
type Outcome struct {
	Agent    string
	ExitCode int

	// WorkspaceChanged reports whether the run left anything behind. Nil means
	// **it could not be determined**, which is a third answer and not a false:
	// a workspace outside git, or a snapshot that failed, cannot be compared, and
	// a caller that read "unknown" as "unchanged" would retry a run that may have
	// done real work.
	WorkspaceChanged *bool
}

// ShouldFailOver decides whether to try the next agent.
//
// It fails **closed**: everything unknown resolves to "do not retry". A retry
// that should not have happened puts a second agent on top of the first one's
// edits; a retry that did not happen costs a command somebody types again. Those
// are not symmetric, so the rule does not pretend they are.
func ShouldFailOver(o Outcome) (bool, string) {
	if o.ExitCode == 0 {
		return false, "the run succeeded"
	}
	if o.WorkspaceChanged == nil {
		return false, "whether the workspace changed could not be determined, so this is treated as work done"
	}
	if *o.WorkspaceChanged {
		return false, "the run changed files, so it is a failed attempt rather than an outage"
	}
	return true, fmt.Sprintf("exited %d having changed nothing", o.ExitCode)
}

// NewID mints an identifier for one routing episode.
//
// Short and time-ordered rather than a UUID: it is read by people in a listing
// beside a timestamp, and it only has to be unique among the episodes on one
// machine. The same shape internal/rescue uses for a session id, for the same
// reason — an id nobody can say out loud is one nobody quotes in a bug report.
//
// Here rather than in one of its two callers because both mint one: the CLI at
// the top of its chain, and the Studio daemon when it accepts a run with a
// fallback behind it. Two spellings of an id whose whole job is to be compared
// is a correlation that silently stops working.
func NewID() string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A machine with no entropy still gets an id: a collision here costs two
		// episodes being grouped, which is a wrong number in one table, while an
		// empty id costs the correlation entirely.
		return time.Now().UTC().Format("150405")
	}
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}
