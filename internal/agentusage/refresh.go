package agentusage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
	"github.com/Amitgb14/sandbox-cli/internal/workspace"
)

// refreshPrompt is the throwaway turn. The reply is discarded — the refreshed
// cache is the entire point — so it is phrased to cost as little as anything can.
const refreshPrompt = "ok"

// refreshCommand is the guest argv for the throwaway turn: claude's headless
// mode, through its bootstrap. A variable so tests can run a fake backend's
// builtins in its place.
var refreshCommand = func(d agents.Descriptor) []string {
	return append(append([]string{}, d.Command...), d.AutonomousArgs(refreshPrompt)...)
}

// refreshTimeout bounds the turn. It includes a cold boot and Claude Code's
// own start-up, and a provider that cannot answer "ok" in this long is one the
// reading would not be current from anyway.
const refreshTimeout = 120

// Refresh gives Claude Code a reason to talk to the server, so the cache it
// keeps for its own /usage is current by the time Read next opens it.
//
// beta.15 ran `claude -p` on the host. The rewrite runs it where claude now
// runs — in a sandbox, with the saved login restored into it and copied back
// out afterwards — so a host with no Claude Code installed can refresh too.
// That changes nothing about the package's two rules. Nothing here writes the
// cache: Claude Code writes it, inside the sandbox, and the login sync copies
// it out exactly as it does after any claude run. Nor is it the live query the
// design rules out (docs/proposals/usage-stats.md): it drives the agent's own
// supported CLI, then reads the file it read before.
//
// Three things make it best-effort, and they are why nothing calls it unasked:
//
//   - It costs one request against the very subscription being measured.
//   - Claude Code decides when to refetch, so a refresh can legitimately leave
//     the stamp where it was. Callers print the age either way.
//   - It needs a sandbox that can reach the provider. A server whose network
//     ceiling is none cannot refresh, and claude's own complaint says so.
//
// The run gets the saved login and nothing from the host environment: an
// ANTHROPIC_API_KEY would make claude bill the key instead, and the windows
// being measured belong to the subscription.
func Refresh(ctx context.Context, c *api.Client) error {
	d, ok := agents.Lookup("claude")
	if !ok {
		return errors.New("refresh usage: no claude descriptor")
	}
	if _, err := os.Stat(filepath.Join(workspace.LoginDir(d), ".claude", ".credentials.json")); err != nil {
		return errors.New("refresh usage: no saved claude login — run `sandbox-cli claude` once and log in")
	}
	env := map[string]string{}
	for _, kv := range d.Env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Env: env})
	if err != nil {
		return fmt.Errorf("refresh usage: %w", err)
	}
	defer c.TerminateSandbox(context.Background(), sb.ID)
	workspace.RestoreLogin(ctx, c, sb.ID, d)
	// Outside /workspace, so the throwaway turn is not filed among the project
	// conversations Claude Code keys by working directory.
	res, err := c.Run(ctx, sb.ID, api.RunRequest{
		Argv: refreshCommand(d), Cwd: "/tmp", TimeoutSecs: refreshTimeout,
	})
	if err != nil {
		return fmt.Errorf("refresh usage: %w", err)
	}
	// Saved even when the turn failed: a refreshed token is worth keeping
	// whatever the answer was.
	workspace.SaveLogin(context.Background(), c, sb.ID, d)
	switch {
	case res.TimedOut:
		return fmt.Errorf("refresh usage: claude gave no answer in %ds", refreshTimeout)
	case res.ExitCode != 0:
		return fmt.Errorf("refresh usage: claude -p exited %d%s", res.ExitCode, detail(append(res.Stdout, res.Stderr...)))
	}
	return nil
}

// detail appends the agent's own complaint to ours, trimmed to a line. Whatever
// went wrong — signed out, no network, a plan with no window — the agent said it
// better than a wrapped exit status can. The last line, not the first: the
// bootstrap talks first (installing, updating) and claude's verdict comes after
// it. It comes from a guest, so it is
// cleaned before it reaches a terminal.
func detail(out []byte) string {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return ""
	}
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	s = termsafe.Clean(s)
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return ": " + s
}

// StaleAfter is how old a reading has to be before asking for a new one is worth
// a request. Claude Code will not refetch inside its own interval anyway, so a
// refresh below this buys nothing and still spends.
const StaleAfter = time.Minute

// NeedsRefresh reports whether a snapshot is old enough to be worth refreshing.
// A snapshot with no stamp at all counts: an age we cannot vouch for is not the
// same as a recent one.
func (s Snapshot) NeedsRefresh(now time.Time) bool {
	if s.FetchedAt.IsZero() {
		return true
	}
	return s.Age(now) >= StaleAfter
}

// RolledOver reports whether this window has passed its reset since the reading
// was taken. The percentage beside it is then the *previous* window's final
// figure — true when it was written, and about the wrong period by the time it
// is read. Windows that report no reset time cannot be placed either side of one
// and so are never called rolled over.
func (w Window) RolledOver(now time.Time) bool {
	return !w.ResetsAt.IsZero() && !w.ResetsAt.After(now)
}
