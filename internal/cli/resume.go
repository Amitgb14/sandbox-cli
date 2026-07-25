package cli

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
)

// expandResumeID rewrites a shortened session id in the guest args into the full
// id the agent needs.
//
// `context list` shows ids abbreviated, the way `git log --oneline` does — but
// git can do that because git resolves prefixes, and the agents do not. Claude
// Code refuses anything that is not a full UUID ("Provided value \"37888763\" is
// not a UUID"), so an abbreviated id was a listing you could read and not use.
// Rather than widening the table to 36-character ids, sandbox-cli resolves the
// prefix itself, which is the kind of glue it exists for.
//
// The rewrite is deliberately timid. It fires only on the token after a known
// resume argument, only when that token is not already a full id, and only when
// exactly one session matches — an ambiguous or unknown value is passed through
// untouched, because the agent's own "no such session" is a better answer than
// silently resuming the wrong conversation. When it does fire, it says so: an
// argument the user typed and an argument the agent received should never differ
// in silence.
func expandResumeID(agent string, guest []string) []string {
	if agent == "" || len(guest) < 2 {
		return guest
	}
	store, known := agentctx.Lookup(agent)
	if !known || len(store.ResumeArgs) == 0 {
		return guest
	}

	// Find a resume argument with a value after it, before doing any I/O: the
	// overwhelmingly common run has no resume flag at all and must pay nothing.
	idx := -1
	for i := 0; i < len(guest)-1; i++ {
		if slices.Contains(store.ResumeArgs, guest[i]) && !strings.HasPrefix(guest[i+1], "-") {
			idx = i + 1
			break
		}
	}
	if idx < 0 {
		return guest
	}

	f, ok := agentctx.Resolve(agent, agentctx.DefaultRoots(), time.Now())
	if !ok {
		return guest
	}
	full, expanded := agentctx.ExpandID(f, guest[idx])
	if !expanded {
		return guest
	}
	out := append([]string{}, guest...)
	out[idx] = full
	fmt.Fprintf(os.Stderr, "sandbox-cli: resuming session %s\n", full)
	return out
}
