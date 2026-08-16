package studioapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/githard"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// Cloning a repository, which is the first thing this API does that **writes to
// the host filesystem and runs a program**.
//
// Everything else here reads (files, transcripts, directory names) or records a
// path somebody already had. This creates a directory and executes git against a
// URL supplied by whoever can reach the port. That is a bigger step than it
// looks, so the refusals are the feature and the clone is the easy part:
//
//   - **The URL's transport is allowlisted, not filtered.** `ext::` is the one
//     that matters: `git clone 'ext::sh -c whoami'` is documented git behaviour
//     and executes a command. A denylist of that one string would be a guess at
//     the shape of the next transport, so only https and ssh are accepted —
//     git://, http:// and file:// are refused too, the first two for being
//     cleartext and the last for reaching this machine's own disk through a
//     mechanism that is supposed to be about a remote.
//   - **No argument may look like a flag.** A URL or a name beginning with `-`
//     becomes an option to git rather than an operand, and `--upload-pack=` is
//     the classic way that ends. `--` separates them as well, so both halves
//     hold.
//   - **The destination goes through the same refusals as adding one.** Its
//     parent must exist and pass sandbox.RefuseUnsafeHostPath — never `/`, never
//     the home directory, never an ancestor of it — and the destination itself
//     must not exist, so a clone can never write into a directory that already
//     holds something.
//   - **Submodules are not fetched.** They name further URLs chosen by the
//     repository rather than by the person asking, which is a second fetch
//     nobody typed.
//   - **No stored credential is spent, and no prompt is opened.** githard already
//     sets `credential.helper=`, so an HTTPS clone cannot quietly draw on the
//     keychain entry somebody stored for their own use, and GIT_TERMINAL_PROMPT=0
//     means it fails with git's own message rather than hanging a request on a
//     password nobody can type. A private HTTPS repository therefore does not
//     clone from here, which is the honest outcome: an HTTP request should not be
//     able to spend a credential the user saved for a terminal.
//
//     **ssh-agent is the exception and is not closed off**: an ssh clone runs as
//     the user and their agent will answer. That is the same authority the person
//     driving this already has, and it is worth knowing before exposing the
//     daemon beyond loopback.
//
// githard is applied for the same reason every other host-side git call here
// uses it: the flags that make git run commands are neutralised, so a hostile
// remote's config cannot execute anything during checkout.

// cloneTimeout bounds one clone. Long enough for a large repository on a slow
// link, short enough that a wrong URL does not hold a request open forever —
// and the HTTP client on the other end has its own patience, so a clone that
// needs longer than this wants a terminal rather than a browser.
const cloneTimeout = 10 * time.Minute

// handleCloneProject is POST /v1/projects/clone.
//
// The second endpoint that refuses to work unauthenticated, and the reason is
// the same one console/input has: it is a *new* reach rather than a new spelling
// of an existing one.
//
// Everything else here resolves a registered id, so the set of directories this
// control plane touches is a file the user can read. This one takes a host path
// and creates a tree under it, from a URL the caller chose, by running git. The
// refusals below bound it hard — an absolute, existing, RefuseUnsafeHostPath'd
// parent, one path segment for the name, no traversal, an allowlisted transport,
// githard's config neutralisation and GIT_TERMINAL_PROMPT=0 — but bounding where
// a thing may write is a different question from who may ask for it, and
// `POST /runs` starting a container in a repository somebody registered is not
// the same act as writing a new one wherever this daemon can reach.
//
// So it needs the server to have been given a token at all. studio.sh always
// generates one, so this costs the ordinary path nothing; what it refuses is a
// daemon deliberately started with no authentication being asked to fetch code
// onto the machine it runs on.
func (s *Server) handleCloneProject(w http.ResponseWriter, r *http.Request) {
	if s.Token == "" {
		writeError(w, http.StatusForbidden, fmt.Errorf(
			"cloning requires the server to have a -token set: it writes a new tree to this host "+
				"from a URL the request names, which is the one thing here that reaches outside the "+
				"repositories you registered.\n"+
				"  Start sandbox-studio-api with -token (or $SANDBOX_STUDIO_TOKEN), or clone it yourself "+
				"and add it by path"))
		return
	}

	var req ProjectCloneRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	url, err := validCloneURL(req.URL)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	dest, err := cloneDestination(req.Parent, req.Name, url)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), cloneTimeout)
	defer cancel()

	// The parent directory: there is no repository yet, and githard's empty-tree
	// lookup simply reports nothing for a path that is not one.
	parent := filepath.Dir(dest)
	args := append(githard.Args(parent),
		// Belt and braces beside the URL check: even if a transport slipped past
		// it, git is told not to run this one.
		"-c", "protocol.ext.allow=never",
		"clone", "--no-recurse-submodules", "--", url, dest,
	)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(append(os.Environ(), githard.Env(parent)...),
		"GIT_TERMINAL_PROMPT=0", // fail rather than hang on a credential nobody can type
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Cleaned up: a failed clone leaves a partial directory, and leaving it
		// behind would make the next attempt fail with "already exists" for a
		// reason that has nothing to do with the URL.
		os.RemoveAll(dest)
		if ctx.Err() == context.DeadlineExceeded {
			writeError(w, http.StatusGatewayTimeout, fmt.Errorf(
				"clone timed out after %s — a repository this large is better cloned in a terminal, then added by path", cloneTimeout))
			return
		}
		// git's own words: it says whether the host is unknown, the repository is
		// private, or authentication failed, and none of that is worth rewriting.
		writeError(w, http.StatusUnprocessableEntity, fmt.Errorf("git clone failed: %s", lastLines(string(out), 6)))
		return
	}

	// Registered through exactly the same path a typed directory takes, so a
	// cloned repository is not a second kind of project — it resolves to a root,
	// gets an id from worktree.RepoID, and is refused if it somehow is not a
	// repository after all.
	rec, err := validateProjectPath(dest)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	if s.Projects == nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("this daemon has no project registry to add to"))
		return
	}
	if err := s.Projects.add(rec); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for _, p := range s.projects() {
		if p.ID == rec.ID {
			writeJSON(w, http.StatusCreated, p)
			return
		}
	}
	writeJSON(w, http.StatusCreated, describeProject(rec))
}

// validCloneURL accepts only the transports that fetch from a remote over an
// authenticated or encrypted channel, and refuses everything else by not being a
// denylist.
func validCloneURL(raw string) (string, error) {
	url := strings.TrimSpace(raw)
	if url == "" {
		return "", fmt.Errorf("a repository URL is required")
	}
	if strings.HasPrefix(url, "-") {
		return "", fmt.Errorf("a URL may not begin with %q: git would read it as an option rather than a repository", "-")
	}
	switch {
	case strings.HasPrefix(url, "https://"):
		return url, nil
	case strings.HasPrefix(url, "ssh://"):
		return url, nil
	case scpLike(url):
		// git@github.com:owner/repo.git — the form everybody pastes.
		return url, nil
	}
	return "", fmt.Errorf(
		"%q is not a supported clone URL: use https://…, ssh://… or git@host:path.\n"+
			"  git://, http:// and file:// are refused as cleartext or local, and ext:: is refused because it executes a command rather than fetching a repository.", url)
}

// scpLike matches git's scp-style syntax without accepting a bare path or a
// transport prefix: there must be a user@host before the colon, and no scheme.
func scpLike(url string) bool {
	if strings.Contains(url, "://") || strings.Contains(url, "::") {
		return false
	}
	at := strings.Index(url, "@")
	colon := strings.Index(url, ":")
	return at > 0 && colon > at+1 && colon < len(url)-1
}

// cloneDestination resolves where the clone lands, applying the same refusals a
// typed path gets before anything is written.
func cloneDestination(parent, name, url string) (string, error) {
	parent = strings.TrimSpace(parent)
	if parent == "" {
		return "", fmt.Errorf("a target directory is required: name the directory to clone into")
	}
	if !filepath.IsAbs(parent) {
		return "", fmt.Errorf("%q is not an absolute path", parent)
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(parent))
	if err != nil {
		return "", fmt.Errorf("no such directory: %s", parent)
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("not a directory: %s", parent)
	}
	// The parent is what the clone writes into, so it is the path that has to be
	// safe — the same check adding a repository applies, for the same reason.
	if err := sandbox.RefuseUnsafeHostPath(real); err != nil {
		return "", err
	}

	if name = strings.TrimSpace(name); name == "" {
		name = repoNameFromURL(url)
	}
	if name == "" {
		return "", fmt.Errorf("could not work out a directory name from %q — give one", url)
	}
	// One path segment, and not a traversal: the name is a directory to create
	// inside the parent, never a way back out of it.
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." || strings.HasPrefix(name, "-") {
		return "", fmt.Errorf("%q is not a directory name: give one path segment, with no separators", name)
	}

	dest := filepath.Join(real, name)
	if _, err := os.Lstat(dest); err == nil {
		return "", fmt.Errorf("%s already exists — clone somewhere else, or add it by path if it is already a checkout", dest)
	}
	return dest, nil
}

// repoNameFromURL is the last path segment without .git, which is what git
// itself would have chosen.
func repoNameFromURL(url string) string {
	trimmed := strings.TrimSuffix(strings.TrimRight(url, "/"), ".git")
	if i := strings.LastIndexAny(trimmed, "/:"); i >= 0 {
		trimmed = trimmed[i+1:]
	}
	return strings.TrimSpace(trimmed)
}

// lastLines keeps the tail of git's output. The interesting line is the last
// one; the rest is progress.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
