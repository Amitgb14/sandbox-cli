package cli

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/creds"
)

// The long-lived-secret warning, ported from beta.15's internal/sandbox. In
// the rewrite the last point at which a secret's name and value are both in
// hand is applyConfig, on the client, so it lives here.

// warnedSecret is where the warning is written. A var so tests can read what was
// printed; deliberately dumb otherwise — it prints, and the run proceeds.
var warnedSecret = func(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
}

// warnedNames records the secret names this process has already warned about, so
// the line is said **once per secret name, per process**. A routed run resolves
// the same `secrets:` block for every attempt it launches, and a process that
// launched many runs used to say the same sentence for each — which is how a
// warning stops being read, the very failure the warning's own design argues
// against. One process is the right scope because it is one command: a routed
// run says it once, an interactive run says it once, and a later run in a new
// process says it again rather than going quiet forever.
//
// Only names that actually warned are recorded — the caller checks the lifetime
// before reaching this map — so a name that resolves short-lived in one task and
// long-lived in the next is still reported on the second.
//
// The lock is not for routing, whose launches are sequential. It is for
// `studioapi`, which calls Session.Start from an HTTP handler, so two concurrent
// POSTs to /runs share this map.
//
// **Per-process is right for a command and wrong for a daemon**, and the
// distinction is invisible from here: a routed run and a Studio server are both one
// Session calling Start N times, and only the caller knows whether that is one
// command or N of them. a daemon outlives every run it starts, so
// this silences the warning for every launch after the first. Harmless today only
// because Studio never shows the line at all (issue #68) — whoever fixes that
// must give the dedupe a per-run scope here, or they will surface a warning that
// appears once and then never again.
var (
	warnedMu    sync.Mutex
	warnedNames = map[string]bool{}
)

// warnLongLivedSecrets reports brokered credentials whose own shape says they
// outlive the run. It is here rather than in `creds` because this is the last
// point at which a secret's name and its value are both in hand, and the value
// must go no further than the container — and it warns rather than refuses for
// the reason `creds.Classify` gives: for some credentials the long-lived form is
// the only form there is.
//
// The message carries the name and the format, never any part of the value, for
// the same reason `audit.SessionMeta` has nowhere to put one: a warning is
// written to a stream somebody may well be logging.
func warnLongLivedSecrets(vars []creds.EnvVar, now time.Time) {
	for _, v := range vars {
		a := creds.Classify(v.Value, now)
		if a.Lifetime != creds.LongLived {
			continue
		}
		if alreadyWarned(v.Name) {
			continue
		}
		warnedSecret("sandbox-cli: secret %s %s. A leaked value stays usable until you "+
			"revoke it; brokering a short-lived credential bounds what that is worth. "+
			"See docs/security/secrets.md.\n", v.Name, a.Detail)
	}
}

// alreadyWarned claims the right to warn about a name, reporting whether somebody
// else got there first. Test-and-set in one critical section, since two
// concurrent runs must not both decide they are the first.
//
// Keyed by name, not by value: a broker that mints a fresh long-lived token per
// task would otherwise defeat the dedupe entirely, and the advice is the same
// every time regardless of which token came back.
func alreadyWarned(name string) bool {
	warnedMu.Lock()
	defer warnedMu.Unlock()
	if warnedNames[name] {
		return true
	}
	warnedNames[name] = true
	return false
}
