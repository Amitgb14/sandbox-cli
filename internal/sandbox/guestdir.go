package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnsureGuestDir creates, on the host, a directory chain that will appear *inside*
// the container — and gives every level the shared-group treatment.
//
// It exists because a bind mount whose **target** does not exist is created by the
// container runtime, as root. Under rootless podman that root is a subordinate uid
// on the host: a `keep-id` mapping puts container uid 0 somewhere in your subuid
// range, so the directory comes back owned by something like 524288 at mode 0755.
// Inside the container that reads as root-owned and not group-writable, and the
// agent — uid 1001 — cannot write a file beside it.
//
// This was reported, not imagined. claude's history mount targets
// `/sandbox/home/.claude/projects/<bucket>`, so podman created `.claude` and
// `projects` as root; Claude Code stores its token at `~/.claude/.credentials.json`,
// directly inside the first of them, and could not write it. The login was asked
// for again on every single run, on Linux, under podman.
//
// **ShareWithSandboxGroup cannot repair it after the fact**, which is why this is a
// separate step rather than more of that one: its `os.Chown`/`os.Chmod` run as the
// invoking user, who does not own a subuid-owned path, so both fail with EPERM —
// best-effort and therefore silently. The only reliable fix is to create the target
// first, so the runtime never has to.
//
// rel is the guest path relative to the mounted root. It is split and rebuilt
// rather than joined blind: a `..` element would walk out of the sandbox-owned
// directory this is allowed to create in, which is the one thing a caller must not
// be able to ask for by accident.
func EnsureGuestDir(root, rel string) {
	if root == "" || rel == "" {
		return
	}
	// Sanitise before creating anything, so a rejected request creates nothing at
	// all rather than the prefix of a path it then refuses to finish.
	var parts []string
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "" || part == "." || part == ".." {
			continue
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return
	}

	// The root is created here rather than assumed to exist, and that is the whole
	// difference between this working and doing nothing: a wrapper assembles its
	// mounts *before* the run path creates the persisted HOME, so on a first run
	// the root is still missing and every Mkdir below would fail with ENOENT —
	// silently, since this is best-effort. Found by running a first install against
	// a clean config root; a test whose t.TempDir already exists cannot see it.
	if err := os.MkdirAll(root, 0o700); err != nil {
		return
	}

	cur := root
	for _, part := range parts {
		cur = filepath.Join(cur, part)
		if err := os.Mkdir(cur, 0o700); err != nil && !os.IsExist(err) {
			return
		}
		warnForeignOwner(cur)
		// Each level, not just the last: the container has to *traverse* every
		// component to reach the mount, so a middle directory left at 0700 with a
		// host uid stops the agent just as effectively as the leaf would.
		ShareWithSandboxGroup(cur)
	}
}

// warnForeignOwner reports a path under a sandbox-owned directory that belongs to
// somebody else, because that is the state EnsureGuestDir prevents but cannot
// undo — and a run that silently keeps failing to persist a login is the exact
// symptom that took a bug report to find.
//
// It names the remedy rather than only the problem. `podman unshare` is required
// because the files are owned by a subordinate uid: a plain `rm -rf` cannot unlink
// inside a directory the invoking user neither owns nor has write access to.
func warnForeignOwner(path string) {
	fi, err := os.Lstat(path)
	if err != nil {
		return
	}
	uid, ok := ownerUID(fi)
	if !ok || uid == os.Getuid() {
		return
	}
	fmt.Fprintf(os.Stderr,
		"sandbox-cli: %s belongs to uid %d, not you — a container runtime created it as root, "+
			"which rootless podman maps to a subordinate uid. The agent cannot write there, so a "+
			"login or history stored beside it will not survive the container. Remove it and this "+
			"run will recreate it correctly:\n  podman unshare rm -rf %s\n",
		path, uid, path)
}
