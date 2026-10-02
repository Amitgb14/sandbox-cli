package creds

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// The security backlog's item 2 was decided by ranking options on what a leak
// costs rather than on how well a value is hidden, and the winner is a practice:
// broker credentials that are worth nothing by the time anybody uses them.
//
// A practice with nothing behind it is the weak part. Someone writes
// `gh auth token`, gets a credential that lasts months, and believes they
// brokered one — the value looks identical either way, and the resolution
// machinery cannot tell them apart. This file is the smallest thing that helps:
// it recognizes credentials whose shape says *long-lived*, so the run can say so
// once, by name.
//
// Two limits, both deliberate, and both stated in the warning itself because a
// reader who assumes otherwise is worse off than one who was never warned:
//
//   - **Silence is not approval.** An unrecognized value yields Unknown, which
//     prints nothing. Most credentials are opaque strings with no lifetime
//     encoded anywhere, and there is no honest way to certify one as short-lived
//     from the outside. This detects a hazard; it does not clear anything.
//   - **It never refuses.** A long-lived credential is often the only kind that
//     exists — `ANTHROPIC_API_KEY` has no ten-minute form — so refusing would
//     refuse the ordinary case. It is the profile asymmetry deliberately not
//     applied: prod refuses what it can offer an alternative to, and here there
//     is none.

// Lifetime is what could be determined about how long a credential stays usable.
type Lifetime int

const (
	// Unknown says nothing was recognized. It is the answer for most values and
	// carries no reassurance — see the note above.
	Unknown Lifetime = iota
	// LongLived says the value's own shape identifies it as a credential that
	// outlives the run, either by a known format or by a deadline it carries.
	LongLived
	// ShortLived is reserved for the formats that state their own expiry and
	// state it soon. Nothing is inferred into it.
	ShortLived
)

// Assessment is one credential's reading.
//
// Detail is a clause that reads directly after "secret NAME " — "begins with
// \"ghp_\" — GitHub personal access tokens, …" or "is a JWT valid for another 30
// days". It states the **evidence**, not a conclusion about whose credential this
// is, so a prefix that later belongs to something else still produces a true
// sentence the reader can dismiss.
//
// It carries no secret material. A matched prefix is a public format marker
// shared by every credential of that kind, which is why echoing it is safe and
// useful; nothing else from the value ever appears.
type Assessment struct {
	Lifetime Lifetime
	Detail   string
}

// longLivedThreshold is how far out a self-declared expiry has to be before the
// credential counts as long-lived. A day is well past "worth nothing by the time
// anybody uses it" while staying clear of the hour-scale tokens that brokers
// actually mint, so a genuine short-lived mint never trips the warning.
const longLivedThreshold = 24 * time.Hour

// A prefix table is a claim about the outside world, and this one can be neither
// completed nor kept current: vendors change formats, and a user's credential may
// come from an issuer nobody here has heard of. Three rules follow, and they are
// what keep a table that is *permanently wrong at the edges* from being harmful:
//
//   - **The JWT reading is the primary signal, not this.** It is a measurement of
//     what the credential says about itself, works for any issuer including ones
//     that do not exist yet, and never rots. It also covers the direction the
//     world is moving — STS, OIDC and workload-identity tokens are JWTs, so the
//     short-lived credentials this feature wants to encourage increasingly state
//     their own expiry. This table only has to cover legacy opaque tokens, which
//     is why it can stay small and rarely change.
//   - **Report the evidence, never an identification.** The warning says the value
//     *begins with* `ghp_` and what that prefix is used for. If the prefix ever
//     means something else, the sentence is still literally true and the reader
//     can dismiss it. Asserting "this is a GitHub token" would be a claim we
//     cannot keep, and being wrong about *which* credential someone holds sends
//     them to the wrong dashboard.
//   - **A prefix earns a row only if it is long, distinctive, and its issuer
//     documents that the credential does not expire on its own.** A short or
//     generic prefix is rejected however common it is: `sk-` was dropped for
//     exactly this, since it matched anything a user prefixed that way and named
//     a vendor while doing it.
//
// What is deliberately *not* here: AWS `AKIA`/`ASIA`, because those identify the
// access key **id**, which is not the secret. Matching them would warn about the
// public half and stay silent on `AWS_SECRET_ACCESS_KEY`, which is the one that
// matters — a warning pointing at the wrong value is worse than none.
//
// Order matters — the first match wins, so a more specific prefix must come
// before the prefix it extends.
var knownLongLived = []struct {
	prefix string
	what   string
}{
	{"github_pat_", "GitHub fine-grained personal access tokens"},
	{"ghp_", "GitHub personal access tokens, which do not expire unless you set an expiry"},
	{"gho_", "GitHub OAuth tokens, which is what `gh auth token` returns"},
	{"glpat-", "GitLab personal access tokens"},
	{"sk-ant-", "Anthropic API keys, which do not expire on their own"},
	{"xoxb-", "Slack bot tokens, which do not expire on their own"},
	{"xoxp-", "Slack user tokens"},
}

// knownShortLived are the prefixes whose issuer *defines* the credential as
// short-lived. They exist so silence can keep meaning one thing: without them a
// recognized-and-fine value and an unrecognized value would both simply fall
// through, and a later reader could not tell which had happened.
var knownShortLived = []struct {
	prefix string
	what   string
}{
	{"ghs_", "GitHub App installation tokens, which expire in an hour"},
}

// Classify reads what a credential's own shape says about its lifetime. It is
// pure: it performs no I/O, contacts no issuer, and returns nothing derived from
// the value beyond the judgement itself.
//
// now is passed rather than read so a JWT's remaining lifetime is a decision the
// caller can pin in a test.
func Classify(value string, now time.Time) Assessment {
	v := strings.TrimSpace(value)
	if v == "" {
		return Assessment{}
	}

	// A JWT states its own deadline, which beats any guess from a prefix — so it
	// is asked first, and a token carrying one is answered by the number rather
	// than by its format.
	if a, ok := classifyJWT(v, now); ok {
		return a
	}

	for _, k := range knownShortLived {
		if strings.HasPrefix(v, k.prefix) {
			return Assessment{Lifetime: ShortLived, Detail: evidence(k.prefix, k.what)}
		}
	}
	for _, k := range knownLongLived {
		if strings.HasPrefix(v, k.prefix) {
			return Assessment{Lifetime: LongLived, Detail: evidence(k.prefix, k.what)}
		}
	}
	return Assessment{}
}

// evidence phrases a prefix match as the observation it actually is. Leading with
// the matched prefix is the whole point: the reader sees what was seen, not a
// verdict about whose credential they hold, and a table entry that goes out of
// date produces a sentence that is still true rather than one that is confidently
// wrong.
func evidence(prefix, what string) string {
	return "begins with " + strconv.Quote(prefix) + " — " + what
}

// classifyJWT reads the `exp` claim out of a JWT payload. The second return is
// false for anything that is not a readable JWT, which includes a JWT whose
// payload is encrypted — the point is to read a deadline that is actually there,
// never to infer one from the format.
func classifyJWT(v string, now time.Time) (Assessment, bool) {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return Assessment{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Assessment{}, false
	}
	var claims struct {
		Exp *float64 `json:"exp"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		return Assessment{}, false
	}
	if claims.Exp == nil {
		// A JWT is only recognized as one once its payload parses, so this is a
		// token that genuinely declares no expiry rather than one we failed to
		// read — which is the strongest form of long-lived there is.
		return Assessment{Lifetime: LongLived, Detail: "is a JWT that declares no expiry"}, true
	}

	exp := time.Unix(int64(*claims.Exp), 0)
	left := exp.Sub(now)
	switch {
	case left <= 0:
		// Already expired. Not a leak worth warning about — it is a run about to
		// fail, which the failure itself explains better than we can.
		return Assessment{Lifetime: ShortLived, Detail: "is a JWT that has already expired"}, true
	case left > longLivedThreshold:
		return Assessment{
			Lifetime: LongLived,
			Detail:   "is a JWT valid for another " + roundedDuration(left),
		}, true
	default:
		return Assessment{Lifetime: ShortLived, Detail: "is a JWT valid for another " + roundedDuration(left)}, true
	}
}

// roundedDuration renders a lifetime at the coarsest unit that still says
// something. The exact second is a property of the credential and reporting it
// precisely would put more of the value's metadata on screen than the reader
// needs.
func roundedDuration(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return strconv.Itoa(int(d.Hours()/24)) + " days"
	case d >= 2*time.Hour:
		return strconv.Itoa(int(d.Hours())) + " hours"
	default:
		return strconv.Itoa(int(d.Minutes())) + " minutes"
	}
}
