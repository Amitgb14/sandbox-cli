// Package githard neutralises the parts of a git repository's own configuration
// that make git run commands.
//
// sandbox-cli runs git **on the host**, inside the repository the sandboxed
// agent has read-write access to. git is a programmable tool: several ordinary
// features name a command in `.git/config` or `.gitattributes` and then execute
// it. That turned routine, unattended work into a container→host escape. All of
// these were reproduced firing during a plain `sandbox-cli run`, with the agent
// writing the files mid-run:
//
//	git add -A         -> filter.<x>.clean (.git/config + .gitattributes)
//	                   -> core.fsmonitor
//	git update-ref     -> .git/hooks/reference-transaction
//	git worktree add   -> filter.<x>.smudge, .git/hooks/post-checkout
//	git show / diff    -> diff.<x>.textconv, diff.<x>.command
//
// GIT_CONFIG_GLOBAL and GIT_CONFIG_SYSTEM are not the answer: every setting above
// can live in the repository's *local* config, which those do not cover and for
// which git offers no equivalent switch. Two levers do work, and this package is
// both of them:
//
//   - `-c key=value` on the command line outranks local config, so the settings
//     that name a command can be overridden individually (Args).
//   - clean/smudge filters cannot be overridden that way, because the driver name
//     comes from `.gitattributes` and is arbitrary — there is no key to name
//     ahead of time. Instead the attribute stack is pointed at an empty tree, so
//     no path ever selects a driver at all (Env).
//
// This deliberately disables *legitimate* filters too. In a git-lfs repository a
// snapshot therefore stores real file content rather than the pointer: larger,
// but the more useful thing for a rescue copy.
//
// Not covered here, on purpose: `worktree.Git` — `sandbox-cli worktree git ...`
// is the user running their own git command in their own repository, where hooks
// firing is the expected behavior rather than a surprise.
package githard

import (
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Args are prepended to a git invocation to override every configurable hook
// point that names a command. `-c` beats the repository's local config, which is
// where an agent would put these.
//
// core.hooksPath points at a non-directory, which git reads as "no hooks here"
// for every hook — including the ones with no dedicated switch, which is what
// makes it better than disabling them one at a time.
func Args() []string {
	return []string{
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.fsmonitor=",
		"-c", "core.sshCommand=",
		"-c", "core.askPass=",
		"-c", "credential.helper=",
		"-c", "core.pager=cat",
		"-c", "core.editor=false",
		"-c", "core.alternateRefsCommand=",
	}
}

// NoExternalDiff stops the diff machinery from running a configured textconv or
// external diff command. Only meaningful for the diff family (`diff`, `show`,
// `log -p`), so callers add it at those sites rather than globally.
func NoExternalDiff() []string { return []string{"--no-textconv", "--no-ext-diff"} }

// Env returns the environment assignments that neutralise clean/smudge filters
// for a git command run in dir, or nothing when the repository's empty-tree id
// cannot be determined.
//
// GIT_ATTR_SOURCE makes git read `.gitattributes` from the named tree instead of
// from the working tree; an empty tree means no path has any attribute, so no
// filter driver is ever selected. The id is hash-algorithm dependent — a sha256
// repository's differs from a sha1 one's, and passing the wrong value makes git
// refuse the command outright — so it is asked for per repository and cached,
// since it never changes for a given one.
//
// Caveat worth knowing: GIT_ATTR_SOURCE arrived in git 2.40 (March 2023). Older
// git ignores it silently and clean filters are *not* neutralised there. The
// other overrides in Args still apply.
func Env(dir string) []string {
	if oid := emptyTreeOID(dir); oid != "" {
		return []string{"GIT_ATTR_SOURCE=" + oid}
	}
	return nil
}

// gitBin is the git executable; a variable so tests can point it elsewhere.
var gitBin = "git"

var emptyTreeCache sync.Map // dir -> empty-tree object id ("" when unknown)

func emptyTreeOID(dir string) string {
	if v, ok := emptyTreeCache.Load(dir); ok {
		return v.(string)
	}
	// `hash-object` without -w computes an id and writes nothing, and with no
	// pathname it applies no attributes — so it is safe to run before the
	// hardening it is being used to build.
	cmd := exec.Command(gitBin, "hash-object", "-t", "tree", os.DevNull)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	oid := ""
	if out, err := cmd.Output(); err == nil {
		oid = strings.TrimSpace(string(out))
	}
	emptyTreeCache.Store(dir, oid)
	return oid
}
