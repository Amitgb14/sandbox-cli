package session

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// PanesUsing reports the panes that still have this host directory mounted.
//
// Keyed on the **path**, not on the branch label, and that is the correction that
// matters. A label records what the launcher asked for at launch; an agent that
// runs `git checkout -b other` inside its worktree puts the two out of sync — the
// desync CLAUDE.md already documents for `land`, where `worktree.Path` falls back
// to a name-derived directory. Keyed on the branch, `worktree rm other` resolved to
// the live container's directory while the guard looked for `sandbox.branch=other`,
// found nothing, and allowed the removal. The mount source is the one answer that
// cannot be stale: it is what the container actually has open.
//
// Still filtered by repo, because a cross-repository listing is a bigger question
// than this one needs and `sandbox.repo` is exact.
//
// "Still has it" is `!Finished()` rather than `Running()`: a paused or restarting
// container is somebody's live run in an odd moment, and an unreadable state is not
// a licence. The first version asked `Running()` and would have deleted a paused
// agent's bind-mount source — which is the accident this whole function exists to
// prevent, arriving through the guard itself.
func PanesUsing(ctx context.Context, l Lister, repoID, dir string) ([]protocol.Pane, error) {
	if l == nil || repoID == "" || dir == "" {
		return nil, nil
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		// The directory is gone, so nothing can be mounting it by this name. Compare
		// the string we were given rather than refusing to answer.
		want = dir
	}
	found, err := l.Containers(ctx, map[string]string{
		sandbox.LabelCLI:  "1",
		sandbox.LabelRepo: repoID,
	})
	if err != nil {
		return nil, err
	}
	var out []protocol.Pane
	for _, c := range found {
		if c.Finished() {
			// Nothing open, nothing to protect. Tidiness is `clean`'s job.
			continue
		}
		if !mountsDir(c, want) {
			continue
		}
		out = append(out, paneFromContainer(c))
	}
	return out, nil
}

// mountsDir reports whether this container has dir as a mount source.
//
// Compared after symlink resolution on both sides, because the two come from
// different places: one from git or a flag, the other from the engine's record of
// what it bound. On macOS /var is a symlink to /private/var, so the same directory
// has two spellings and a string compare misses half the time — the same reason
// internal/worktree resolves its paths as soon as the directory exists.
//
// An exact match, not a prefix: a container whose workspace is a *subdirectory* of
// this worktree would still be writing under it, but so would one whose workspace
// is the repository root containing it, and treating a parent as "in use" would
// refuse every removal. Exact is the claim that is true.
func mountsDir(c runtime.ContainerInfo, want string) bool {
	for _, m := range c.Mounts {
		if m.Source == want {
			return true
		}
		if resolved, err := filepath.EvalSymlinks(m.Source); err == nil && resolved == want {
			return true
		}
	}
	return false
}

// RefuseWorktreeInUse is the one rule its callers share: a directory a sandbox
// still has mounted is not removed.
//
// It exists because there were three callers and two answers. `fleet clean` skipped
// a branch whose *fleet* container was running; the `worktree rm` command and
// Studio's delete handler checked nothing — so Studio could pull the bind-mount
// source out from under an agent that was writing to it. A gate some callers apply
// is a gate the others will be found missing later, which is the same shape as
// `internal/fleet`'s rule about `sandbox.Options`.
//
// **`--force` does not override this.** That is the decision in the function, and
// it is deliberately not symmetric with the dirty check: `--force` means "I accept
// losing the uncommitted work I can see", and the work here is being written by a
// process that is still running, so nobody can see all of it yet. Killing the pane
// implicitly would be the other way to resolve it and is worse — the flag would then
// mean "stop my agent", which is not what anyone types it for. So it refuses, and
// names the pane to stop.
//
// `dir` is the worktree directory, resolved by the caller. Passing the path rather
// than the branch is also what keeps the message honest: a caller that has not found
// a worktree has nothing to pass, so the refusal cannot fire where there is no
// worktree at all.
func RefuseWorktreeInUse(ctx context.Context, l Lister, repoID, branch, dir string) error {
	live, err := PanesUsing(ctx, l, repoID, dir)
	if err != nil {
		// Unanswerable, not refused. The engine being unreachable is not evidence
		// that a pane is running, and turning it into one would block a removal for
		// a reason that has nothing to do with the worktree. The dirty check still
		// stands, and it is the one that protects the files.
		return nil
	}
	if len(live) == 0 {
		return nil
	}

	var b strings.Builder
	what := "a sandbox is"
	if len(live) > 1 {
		what = fmt.Sprintf("%d sandboxes are", len(live))
	}
	fmt.Fprintf(&b, "%s still running in the worktree for %q, so removing it would take the directory out from under it:",
		what, termsafe.Clean(branch))
	for _, p := range live {
		// Both through termsafe: a pane id is a container *label* value, which is
		// text a repository can influence, and these lines are aligned — so an
		// uncleaned one could forge a row beside it. The container name next to it
		// was cleaned from the start, which is what made the omission visible.
		fmt.Fprintf(&b, "\n  %s  %s", termsafe.Clean(p.ID), termsafe.Clean(p.ContainerName))
	}
	b.WriteString("\n  Stop it first: sandbox-cli kill " + termsafe.Clean(live[0].ContainerName))
	// Said explicitly, because --force overriding the *other* refusal this command
	// makes is exactly what invites trying it here.
	b.WriteString("\n  --force does not cover this: it discards work you can see, and an agent that is still running has not finished writing.")
	return fmt.Errorf("%s", b.String())
}
