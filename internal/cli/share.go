package cli

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// sharedTarget is where --share mounts the shared host directory inside the
// container. Deliberately short and well-known: the whole point is that it can
// be named in a sentence to an agent ("write the contract to
// /shared/openapi.yaml") without anyone learning a config schema first.
const sharedTarget = "/shared"

// sharedReadme is seeded into the shared directory the first time --share
// creates it. A bind mount is invisible to an agent that was never told it
// exists; a README inside the directory it is about to list is the cheapest way
// to make the channel self-describing to both the agent and the user who later
// wonders what this folder is.
const sharedReadme = `# Shared sandbox directory

This directory is mounted at ` + "`" + sharedTarget + "`" + ` inside every sandbox started with
` + "`--share`" + `, and lives on the host at ` + "`~/.config/sandbox/shared`" + `.

It is the one place two sandboxes can exchange files. Everything else a sandbox
can see is scoped to its own project, so agents working in different projects
(or different git worktrees) have no other way to hand something over.

Use it for artifacts that cross a boundary — an API contract, a JSON schema, a
generated client, a note from one agent to another:

    # in the sandbox that produces it
    write the API contract to /shared/openapi.yaml

    # in the sandbox that consumes it
    read /shared/openapi.yaml and implement the endpoints

Files written here persist on the host after the containers exit, and are shared
read-write by every sandbox using --share, so treat it as scratch space with one
owner per file rather than a database. For versioned handover, keep a git repo
in here and push to it from both sides.
`

// seedSharedReadme writes the explainer into dir unless something is already
// there. Best-effort by design: the mount is the part that matters and has
// already been arranged by the caller, so a failure to write a README must never
// fail the run.
func seedSharedReadme(dir string) {
	writeReadmeOnce(filepath.Join(dir, "README.md"), sharedReadme)
}

// writeReadmeOnce creates p with text, and does nothing at all if anything is
// already there. O_EXCL is the whole point and is not interchangeable with a
// Stat-then-write: os.Stat *follows* symlinks, so a dangling link planted in
// this directory failed the "already there?" check and os.WriteFile then
// followed the same link and created its target — an agent could plant
// `ln -s ~/.zshenv /shared/README.md` and have the next run create that file
// outside the shared directory. The container has this directory read-write by
// design (that is what --share is), so it is attacker-controlled input, and
// O_EXCL makes the kernel refuse an existing path without ever following it.
//
// Still best-effort: every error is discarded, because a README that could not
// be written must never fail a run the mount has already been arranged for.
func writeReadmeOnce(p, text string) {
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return // already exists, is a symlink, or the directory is not writable
	}
	defer f.Close()
	_, _ = f.WriteString(text)
}

// seedShareNamespaceReadme writes a namespace-specific explainer into dir
// unless something is already there. Deliberately not sharedReadme: that text
// says the directory "is mounted at /shared" and "lives on the host at
// ~/.config/sandbox/shared", both false for a leaf namespace. Best-effort by
// design, same as seedSharedReadme: a failed README must never fail the run.
func seedShareNamespaceReadme(dir, name, target string) {
	readme := fmt.Sprintf(`# Shared sandbox namespace: %s

This directory is mounted at `+"`%s`"+` inside sandboxes started with
`+"`--share=%s`"+`, and lives on the host at `+"`~/.config/sandbox/shared/%s`"+`.

A namespace prevents *collisions*, not access. Two sandboxes using different
names can both write `+"`notes.md`"+` without overwriting each other, which is what
it is for. Files persist on the host after the containers exit.

It is NOT an isolation boundary, and nothing here is private. Any sandbox
started with a bare `+"`--share`"+` has the whole shared directory mounted
read-write and can therefore read and modify every namespace inside it,
including this one. Treat anything written here as visible to every sandbox on
this machine that asks for sharing.
`, name, target, name, name)
	writeReadmeOnce(filepath.Join(dir, "README.md"), readme)
}

// shareNameRE allowlists a --share namespace to a single path segment: a
// leading letter or digit rules out ".", ".." and ".hidden"; the character
// class rules out "/" and "\" (which would add path components), ":" (which
// would corrupt the colon-joined mount string that sandbox.parseMount splits
// later) and "," (which sandbox.ValidateMountPath refuses because docker's
// --mount syntax is comma-separated).
var shareNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// validateShareName refuses a --share namespace that is unsafe to become part
// of a host path. It refuses rather than sanitizes: this repo sanitizes
// user input for display names (e.g. worktree branches), but a namespace here
// becomes a bind-mount source, so a silently rewritten name would mount
// somewhere other than what the user typed.
func validateShareName(name string) error {
	if name == "" {
		return fmt.Errorf("empty namespace name; use a bare --share for the shared root")
	}
	if !shareNameRE.MatchString(name) {
		return fmt.Errorf("invalid namespace name %q: use 1-64 characters from [A-Za-z0-9._-] starting with a letter or digit", name)
	}
	// Belt-and-braces: unreachable once the regexp above has matched (it already
	// rules out "/", "..", and a leading "."), kept because filepath.IsLocal is
	// the purely lexical check this repo uses elsewhere for "does this path stay
	// inside its parent", and a second independent check costs nothing here.
	if !filepath.IsLocal(name) {
		return fmt.Errorf("namespace name %q must stay inside the shared directory", name)
	}
	return nil
}

// shareNamespaceDir resolves and creates the host directory for a --share
// namespace, returning the (unresolved) mount source and the container target
// to mount it at. name has already been validated by the caller's use of
// validateShareName below, but every step here still assumes it could be
// hostile, since this is the one place that turns user input into a host
// path handed to docker.
func shareNamespaceDir(root, name string) (hostDir, target string, err error) {
	if err := validateShareName(name); err != nil {
		return "", "", err
	}

	// Create the leaf without ever following a symlink out of the shared root:
	// os.Root refuses any path component that resolves outside root, so a
	// symlink planted in the shared dir cannot make the mount source escape.
	r, err := os.OpenRoot(root)
	if err != nil {
		return "", "", fmt.Errorf("opening shared dir %s: %w", root, err)
	}
	defer r.Close()
	if err := r.MkdirAll(name, 0o700); err != nil {
		return "", "", fmt.Errorf("creating shared namespace %q: %w", name, err)
	}

	// os.Root refuses a symlink leaf only when it resolves OUTSIDE the root, so
	// a *relative* link that stays inside is accepted — and `ln -s . work` then
	// resolves to the root itself, which handed the whole shared directory back
	// as the mount source. The container has this directory read-write whenever
	// any run uses a bare --share, so it plants the link and a later
	// --share=work mounts every other namespace read-write. Refuse the symlink
	// itself: the namespace must be a real directory, created here or by a
	// previous run, and nothing else.
	fi, err := r.Lstat(name)
	if err != nil {
		return "", "", fmt.Errorf("checking shared namespace %q: %w", name, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Errorf("namespace %q is a symlink; refusing to mount what it points at", name)
	}
	if !fi.IsDir() {
		return "", "", fmt.Errorf("namespace %q exists and is not a directory", name)
	}

	hostDir = filepath.Join(root, name)

	// Second, independent check on the path string we are about to hand
	// docker: docker resolves the mount source on the host, not through
	// os.Root, so containment has to hold for the resolved string too.
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("resolving shared dir %s: %w", root, err)
	}
	resolved, err := filepath.EvalSymlinks(hostDir)
	if err != nil {
		return "", "", fmt.Errorf("resolving shared namespace %s: %w", hostDir, err)
	}
	// Identity, not containment. "somewhere under the root" is the wrong
	// question and is what let `ln -s .` through: underDir returns true when the
	// resolved path IS the root, and true for a link to a sibling namespace. The
	// only acceptable answer is that this path is the namespace directory.
	if want := filepath.Join(resolvedRoot, name); resolved != want {
		return "", "", fmt.Errorf("namespace %q resolves to %s, not %s", name, resolved, want)
	}

	// Every host path that gets bind-mounted goes through this refusal
	// (CLAUDE.md): sandbox.BuildSpec does not apply it to ExtraMounts, so this
	// is the only place it happens for --share.
	if err := sandbox.RefuseUnsafeHostPath(resolved); err != nil {
		return "", "", err
	}

	target = path.Join(sharedTarget, name)
	// Hand back the RESOLVED path, not the one the user typed. Docker resolves
	// the mount source itself at run time, so passing the unresolved string
	// meant the path we checked and the path docker acts on were two different
	// things — and the shared directory is writable by any bare --share peer,
	// so the difference is attacker-supplied. Passing `resolved` is strictly no
	// worse (it is the same directory) and removes the symlinked-leaf variable
	// entirely.
	//
	// It does NOT close the race: between this return and docker's mount, a
	// concurrent container can still swap the leaf. Docker takes a path string
	// rather than an fd, so there is no handle to pass and nothing here can make
	// the check and the use atomic. Recorded as residual in
	// docs/security/open-items.md §8 rather than papered over.
	return resolved, target, nil
}

// shareValue is the pflag.Value behind --share, which takes an OPTIONAL value.
// pflag resolves `--flag=x` to x, and a bare `--flag` to NoOptDefVal; a value is
// optional only when NoOptDefVal is non-empty, which is also what stops the
// agent wrappers' splitter (run.go:91) from eating the next token. Reporting
// "bool" as the Type keeps the help line identical to the old BoolVar (pflag
// only prints a `[=…]` value hint for non-bool types).
//
// Every spelling strconv.ParseBool accepts (1, t, T, true, TRUE, True, 0, f,
// F, false, FALSE, False) is reserved as an on/off spelling, so none of them
// can ever be namespace names; every other string that passes
// validateShareName is one. This has to be checked with ParseBool, not a
// literal "true"/"false" switch: --share used to be a pflag.BoolVar, so
// scripts already in the wild pass spellings like --share=0 or --share=FALSE
// expecting sharing to turn off, and a namespace named "0" would silently
// re-enable the cross-sandbox channel they asked to disable.
type shareValue struct{ rf *runFlags }

// offSpellings are the words a user reaches for to turn a flag off that
// strconv.ParseBool does NOT accept. Reserving them is the difference between a
// wrong command failing and a wrong command doing the opposite of what it says:
// `--share=no` was a hard parse error under the old BoolVar, and without this it
// becomes a namespace literally named "no" with sharing switched ON — an
// operator disabling the cross-project channel would silently enable it. The
// on-spellings are reserved for symmetry, so `--share=yes` cannot mean a
// namespace either.
var offSpellings = map[string]bool{
	"no": true, "n": true, "off": true,
	"yes": true, "y": true, "on": true,
}

func (v *shareValue) Set(s string) error {
	if b, err := strconv.ParseBool(s); err == nil {
		v.rf.share, v.rf.shareName = b, ""
		return nil
	}
	if offSpellings[strings.ToLower(s)] {
		return fmt.Errorf("%q is reserved (as are no, n, off, yes, y, on and the true/false spellings) "+
			"because it reads as switching the flag rather than naming a namespace: "+
			"omit --share entirely to disable sharing, use a bare --share for the shared root, "+
			"or pick a distinguishable name such as %q", s, s+"-ns")
	}
	if err := validateShareName(s); err != nil {
		return err
	}
	v.rf.share, v.rf.shareName = true, s
	return nil
}

func (v *shareValue) String() string {
	// Called by pflag at registration time (to capture DefValue) before rf is
	// ever set on a real flag instance, so this must tolerate a nil rf.
	if v == nil || v.rf == nil {
		return "false"
	}
	if !v.rf.share {
		return "false"
	}
	if v.rf.shareName == "" {
		return "true"
	}
	return v.rf.shareName
}

func (v *shareValue) Type() string { return "bool" }

// IsBoolFlag keeps pflag from printing a bogus "(default …)" suffix on the
// help line, the same as it does for a real BoolVar.
func (v *shareValue) IsBoolFlag() bool { return true }
