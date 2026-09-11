package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// RunningPanesOn reports the panes still working in a worktree.
//
// Asked of the **engine**, not of the catalog, and that is the point: a daemon
// need not be running for the question to have an answer, and the engine is the
// thing that knows. The catalog would also be a snapshot of whenever it was last
// refreshed, which for "is something writing to this directory right now" is the
// wrong kind of answer.
//
// Scoped by repository as well as branch, because a branch name is not unique
// across repositories and `sandbox.branch` alone would match another project's
// agent working on a branch of the same name.
func RunningPanesOn(ctx context.Context, l Lister, repoID, branch string) ([]protocol.Pane, error) {
	if l == nil || repoID == "" || branch == "" {
		return nil, nil
	}
	found, err := l.Containers(ctx, map[string]string{
		sandbox.LabelCLI:    "1",
		sandbox.LabelRepo:   repoID,
		sandbox.LabelBranch: branch,
	})
	if err != nil {
		return nil, err
	}
	var out []protocol.Pane
	for _, c := range found {
		if !c.Running() {
			// An exited container on this branch is not writing to the worktree, so it
			// is not a reason to refuse. The question is about the directory, not about
			// tidiness — `clean` is what reaps those.
			continue
		}
		out = append(out, paneFromContainer(c))
	}
	return out, nil
}

// RefuseWorktreeInUse is the one rule three callers now share: a worktree with a
// live pane in it is not removed.
//
// It exists because there were three callers and two answers. `fleet clean`
// already skipped a branch whose container was running; the `worktree rm` command
// and Studio's delete handler did not — so Studio could pull the bind-mount source
// out from under an agent that was writing to it, and the agent would go on writing
// into a directory that no longer had a name. A gate that two of three callers
// apply is a gate that the third will be found missing later, which is the same
// shape as `internal/fleet`'s rule about `sandbox.Options`.
//
// **`--force` does not override this.** That is the decision in the function, and
// it is deliberately not symmetric with the dirty check: `--force` means "I accept
// losing the uncommitted work I can see", and the work here is being written by a
// process that is still running, so nobody can see all of it yet. Killing the pane
// implicitly would be the other way to resolve it and is worse — the flag would then
// mean "stop my agent", which is not what anyone types it for. So it refuses, and
// names the pane to stop.
func RefuseWorktreeInUse(ctx context.Context, l Lister, repoID, branch string) error {
	live, err := RunningPanesOn(ctx, l, repoID, branch)
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
		fmt.Fprintf(&b, "\n  %s  %s", p.ID, termsafe.Clean(p.ContainerName))
	}
	b.WriteString("\n  Stop it first: sandbox-cli kill " + termsafe.Clean(live[0].ContainerName))
	// Said explicitly, because --force overriding the *other* refusal this command
	// makes is exactly what invites trying it here.
	b.WriteString("\n  --force does not cover this: it discards work you can see, and an agent that is still running has not finished writing.")
	return fmt.Errorf("%s", b.String())
}
