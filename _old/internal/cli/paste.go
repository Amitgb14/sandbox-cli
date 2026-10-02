package cli

import (
	"os"
	"path/filepath"
	"strings"
)

// Pasting an image into an agent running in the sandbox.
//
// On the host, "copy image → paste" works because the agent and the image are on
// the same filesystem: the terminal turns a copied/dragged file into its absolute
// path, the agent reads that path, and the image is attached. Inside the sandbox
// only the path crosses the boundary. /Users/you/Desktop/shot.png means nothing
// in a container where the sole host mount is the project, so the agent reports a
// missing file and the paste looks like it silently did nothing.
//
// --paste closes exactly that gap: the directories images normally come from are
// bind-mounted read-only *at their own host paths*, so the pasted path resolves
// to the same bytes it names on the host. This is the same trick the worktree
// code already relies on for the parent .git directory (see newSession).
//
// It is opt-in because it is real extra reach — an agent that can read
// ~/Downloads can read every file in it, not only the one you pasted. Read-only
// is not negotiable here for the same reason: attaching an image never needs to
// write, and the mount is wide.
//
// What this does not fix: an image copied as raw bits rather than as a file (a
// browser's "Copy Image", a screenshot straight to the clipboard). That never
// produces a path at all — the agent reads the OS clipboard directly, and the
// container has no clipboard to read. See the clipboard bridge in the base image
// Dockerfile for why the read direction stays unimplemented.

// pasteDirNames are the directories an image pasted into a terminal most often
// comes from, relative to the host home. Screenshots land in Desktop on macOS
// and in Pictures on the common Linux desktops; Downloads covers anything saved
// from a browser. Deliberately a short, boring list: every entry is a directory
// the sandbox can suddenly read, so this is not the place to be generous.
var pasteDirNames = []string{"Desktop", "Downloads", "Pictures"}

// pasteDirs returns the paste directories that exist under home, in the fixed
// order above. Directories that are absent are skipped rather than reported: a
// machine with no ~/Desktop is normal, not misconfigured, and docker would
// otherwise create the missing path (as root, on Linux) to satisfy the bind.
//
// Everything is resolved under home and nothing outside it is ever returned,
// which is what keeps this away from paths the container needs for itself — most
// pointedly /tmp, where the host's copy mounted over the container's would shadow
// a directory the guest expects to be its own and writable.
func pasteDirs(home string) []string {
	if home == "" {
		return nil
	}
	// Resolve the home itself before comparing against it. os.UserHomeDir reports
	// the path as configured, which on some setups runs through a symlink (/home
	// -> /usr/home, /Users -> /System/Volumes/Data/Users); without this the
	// containment check below would reject every candidate and --paste would
	// quietly mount nothing.
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	var out []string
	for _, name := range pasteDirNames {
		p := filepath.Join(home, name)
		fi, err := os.Stat(p)
		if err != nil || !fi.IsDir() {
			continue
		}
		// Guard the invariant the comment above claims, rather than trusting the
		// literal list: a symlinked ~/Desktop pointing at / would otherwise mount
		// the whole host filesystem.
		real, err := filepath.EvalSymlinks(p)
		if err != nil || !strings.HasPrefix(real, home+string(filepath.Separator)) {
			continue
		}
		out = append(out, real)
	}
	return out
}

// pasteMounts renders pasteDirs as read-only same-path mount specs for
// Options.ExtraMounts, returning the directories alongside them so the caller can
// name what it mounted without repeating the lookup.
func pasteMounts(home string) (mounts, dirs []string) {
	dirs = pasteDirs(home)
	mounts = make([]string, 0, len(dirs))
	for _, d := range dirs {
		mounts = append(mounts, d+":"+d+":ro")
	}
	return mounts, dirs
}
