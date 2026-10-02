package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// A checkpoint is the sandbox's working tree, committed and fetched to the host
// while the run is still going, so a VM that dies takes minutes of work with it
// rather than the whole run. A sandboxd restart, a host reboot or a crashed VM
// all end a sandbox with its disk; bring-back cannot run after that, and the
// checkpoint is what is left.
//
// The rule beta.15's rescue kept carries over: a checkpoint never touches what
// the agent is working with. It is built through a private index, so the
// agent's index, HEAD and branches are as it left them, and it lives at
// refs/sandbox/checkpoint in the guest — a ref `git branch` does not list —
// so the agent does not find a branch it never made. On the host it goes to
// refs/sandbox/checkpoints/<sandbox>, and no branch is ever moved.

// guestCheckpointRef is where the checkpoint commit lives in the guest. The
// bundle endpoint is asked for "sandbox/checkpoint", which git resolves to
// refs/sandbox/checkpoint before it would look in refs/heads (gitrevisions).
const (
	guestCheckpointRef  = "refs/sandbox/checkpoint"
	guestCheckpointName = "sandbox/checkpoint"
)

// CheckpointRefPrefix is the host namespace for checkpoints.
const CheckpointRefPrefix = "refs/sandbox/checkpoints/"

// checkpointScript commits the working tree through a private index, onto the
// agent's HEAD, and moves the guest checkpoint ref — unless nothing changed
// since the last checkpoint, which it reports as "unchanged". The private index
// starts as a copy of the agent's, so unchanged files are not re-hashed.
const checkpointScript = `set -e
cd /workspace
idx=$(mktemp)
trap 'rm -f "$idx"' EXIT
if [ -f .git/index ]; then cp .git/index "$idx"; else rm -f "$idx"; fi
export GIT_INDEX_FILE="$idx"
git add -A
tree=$(git write-tree)
parent=$(git rev-parse -q --verify HEAD)
prev=$(git rev-parse -q --verify ` + guestCheckpointRef + ` || true)
if [ -n "$prev" ] && [ "$(git rev-parse "$prev^{tree}")" = "$tree" ] && [ "$(git rev-parse "$prev^")" = "$parent" ]; then
  echo unchanged
  exit 0
fi
commit=$(git -c user.name=sandbox -c user.email=sandbox@localhost commit-tree "$tree" -p "$parent" -m "sandbox: checkpoint")
git update-ref ` + guestCheckpointRef + ` "$commit"
echo "$commit"`

// Checkpoint takes one checkpoint of the session's sandbox and fetches it into
// refs/sandbox/checkpoints/<sandbox>. It returns the ref and whether anything
// new was fetched; a working tree that has not changed since the last one
// costs a git add and nothing else.
func Checkpoint(ctx context.Context, c *api.Client, s Session) (ref string, fetched bool, err error) {
	res, err := c.Run(ctx, s.Sandbox, api.RunRequest{Argv: []string{"sh", "-c", checkpointScript}, TimeoutSecs: 120})
	if err != nil {
		return "", false, err
	}
	if res.ExitCode != 0 {
		return "", false, fmt.Errorf("checkpointing in the sandbox: %s", strings.TrimSpace(string(res.Stderr)))
	}
	ref = CheckpointRefPrefix + s.Sandbox
	if strings.TrimSpace(string(res.Stdout)) == "unchanged" {
		return ref, false, nil
	}

	tmp, err := os.CreateTemp("", "sbx-ckpt-*.bundle")
	if err != nil {
		return "", false, err
	}
	defer os.Remove(tmp.Name())
	err = c.GetWorkspaceBundle(ctx, s.Sandbox, s.Base, guestCheckpointName, tmp)
	tmp.Close()
	if err != nil {
		return "", false, err
	}
	// From the guest, so checked the way bring-back checks: it verifies against
	// this repository and carries exactly the one ref asked for.
	if _, err := Git(s.Repo, "bundle", "verify", "--quiet", tmp.Name()); err != nil {
		return "", false, fmt.Errorf("the checkpoint from the sandbox does not verify: %w", err)
	}
	heads, err := Git(s.Repo, "bundle", "list-heads", tmp.Name())
	if err != nil {
		return "", false, err
	}
	lines := strings.Split(strings.TrimSpace(heads), "\n")
	if len(lines) != 1 || !strings.HasSuffix(lines[0], " "+guestCheckpointRef) {
		return "", false, errors.New("the checkpoint from the sandbox does not carry exactly the checkpoint ref; not fetching it")
	}
	if _, err := Git(s.Repo, "fetch", "--quiet", "--no-tags", "--no-recurse-submodules", "--force", tmp.Name(),
		guestCheckpointRef+":"+ref); err != nil {
		return "", false, err
	}
	return ref, true, nil
}
