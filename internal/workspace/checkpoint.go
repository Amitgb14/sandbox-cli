package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

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

// Checkpoints calls take every interval until the returned stop is called, and
// stop reports the last failure if no success followed it: one checkpoint that
// failed while the VM was busy is not worth reporting once a later one worked.
// taken is called after each checkpoint that fetched something new, so a caller
// can record it before the next one — a CLI that is itself killed should leave
// the latest checkpoint findable.
//
// One loop serves every caller that stays connected to a run (run, attach and
// each fleet task), so the rule about which failures surface is the same in
// all of them. A run nobody is connected to has nothing driving this. That is
// why `run --detach` refuses --checkpoint-every rather than accepting it and
// taking none.
func Checkpoints(ctx context.Context, every time.Duration, take func(context.Context) (string, bool, error), taken func(ref string)) (stop func() error) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		var last error
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				done <- last
				return
			case <-t.C:
			}
			ref, fetched, err := take(ctx)
			switch {
			case err != nil:
				// A checkpoint cut short by stop is not a failure of the run.
				if ctx.Err() == nil {
					last = err
				}
			default:
				last = nil
				if fetched && taken != nil {
					taken(ref)
				}
			}
		}
	}()
	return func() error {
		cancel()
		return <-done
	}
}

// SessionCheckpoints checkpoints a session's sandbox every interval, recording
// each new checkpoint in the session record, then calling also (if not nil)
// with its ref — which is how a mirror copies it off the machine.
func SessionCheckpoints(ctx context.Context, c *api.Client, sess *Session, every time.Duration, also func(ref string)) (stop func() error) {
	return Checkpoints(ctx, every,
		func(ctx context.Context) (string, bool, error) { return Checkpoint(ctx, c, *sess) },
		func(ref string) {
			sess.Checkpoint, sess.CheckpointAt = ref, time.Now().UTC()
			_ = sess.Save()
			if also != nil {
				also(ref)
			}
		})
}
