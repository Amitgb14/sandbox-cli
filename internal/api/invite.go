package api

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// InviteBase is a hosted Studio's address as an invite link's base: an
// origin, nothing after it. The link is that base, "/#key=" and the key's
// secret; the key rides in the fragment, which a browser never sends to a
// server, and hosted Studio's page exchanges it for a session
// (docs/studio.md#hosted-studio).
func InviteBase(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSuffix(raw, "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("--invite-url %q: want Studio's address, https://host[:port]", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

// AdminInviteNote is said beside an admin key's invite link: hosted Studio
// takes one only when its operator allowed it.
const AdminInviteNote = "An admin's invite link signs in only where Studio runs with sandbox-cli studio host --allow-admin."

// ParseKeyExpiry reads how long a key lasts: "never" (or "0") is 0, and a
// duration is Go's ("12h", "90m") or whole days ("7d"). At least a minute.
func ParseKeyExpiry(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "never" || s == "0" {
		return 0, nil
	}
	var d time.Duration
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return 0, fmt.Errorf("%q: a number of days, e.g. 7d", s)
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, fmt.Errorf("%q: never, a number of days (7d) or a duration (12h)", s)
		}
	}
	if d < time.Minute {
		return 0, errors.New("a key's expiry is at least a minute away; to end a key now, revoke it")
	}
	return int(d / time.Second), nil
}
