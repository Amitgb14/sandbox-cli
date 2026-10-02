package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// Code goes into a sandbox as a git bundle and comes back out as one. That is
// the whole workspace model for remote sandboxes, and the default everywhere:
// the host never mounts its repository into a guest, so nothing the agent
// writes is a file the host's git later reads or runs (hooks, config, the
// object store). The guest's git does all the work here; the host only moves
// bytes. Verifying a bundle that comes back, and fetching it into
// refs/sandbox/… through githard, is the client's job — the server cannot do it
// for a repository it never sees.

// maxBundle bounds a bundle in either direction.
const maxBundle = 4 << 30

const (
	bundleIn  = "/tmp/.sbx-workspace-in.bundle"
	bundleOut = "/tmp/.sbx-workspace-out.bundle"
)

// refRE is a conservative subset of git's ref-name rules: what a branch name
// typically is, and nothing that could read as an option or a range.
var refRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)

// revRE is a full or abbreviated commit id.
var revRE = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

func validRef(s string) bool {
	return refRE.MatchString(s) && !strings.Contains(s, "..") && !strings.Contains(s, "//") &&
		!strings.HasSuffix(s, ".lock") && !strings.HasSuffix(s, "/") && !strings.HasSuffix(s, ".")
}

// execIn runs argv to completion in a sandbox, feeding stdin and copying stdout,
// and returns the exit code and up to 4 KiB of stderr.
func (s *Server) execIn(ctx context.Context, id string, argv []string, stdin io.Reader, stdout io.Writer) (int, string, error) {
	if stdout == nil {
		stdout = io.Discard
	}
	var stderr limitedBuffer
	p, err := s.Backend.Start(ctx, id, backend.ProcSpec{Argv: argv}, stdout, &stderr)
	if err != nil {
		return 0, "", err
	}
	in := p.Stdin()
	if stdin != nil {
		if _, err := io.Copy(in, stdin); err != nil {
			_ = p.Signal("KILL")
			p.Wait()
			return 0, "", err
		}
	}
	_ = in.Close()
	return p.Wait(), stderr.String(), nil
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := 4096 - b.Len(); room > 0 {
		if len(p) > room {
			b.Buffer.Write(p[:room])
		} else {
			b.Buffer.Write(p)
		}
	}
	return len(p), nil
}

// gitMessage is git's own stderr, made safe to hand back: it comes from inside
// the guest.
func gitMessage(stderr string) string {
	return termsafe.Clean(strings.TrimSpace(stderr))
}

func (s *Server) putWorkspace(w http.ResponseWriter, r *http.Request) {
	if !s.Backend.Capabilities()[api.CapWorkspaceBundle] {
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, "this endpoint cannot clone a workspace in")
		return
	}
	branch := r.URL.Query().Get("branch")
	if !validRef(branch) {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "branch: a git branch name is required")
		return
	}
	rec, ok := s.live(w, r)
	if !ok {
		return
	}
	id := rec.snapshot().ID
	ctx := r.Context()
	body := http.MaxBytesReader(w, r.Body, maxBundle)

	code, msg, err := s.execIn(ctx, id, []string{"sh", "-c", "cat > " + bundleIn}, body, nil)
	if err != nil {
		writeBackendErr(w, err)
		return
	}
	if code != 0 {
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "storing the bundle in the sandbox failed: "+gitMessage(msg))
		return
	}
	// The bundle carries HEAD — the client must not create a branch in its own
	// repository just to send it — and the guest names it.
	code, msg, err = s.execIn(ctx, id, []string{"git", "clone", "--quiet", "--no-hardlinks", bundleIn, "/workspace"}, nil, nil)
	if err == nil && code == 0 {
		code, msg, err = s.execIn(ctx, id, []string{"git", "-C", "/workspace", "checkout", "--quiet", "-B", branch}, nil, nil)
	}
	_, _, _ = s.execIn(ctx, id, []string{"rm", "-f", bundleIn}, nil, nil)
	if err != nil {
		writeBackendErr(w, err)
		return
	}
	if code != 0 {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "cloning the bundle in the sandbox failed (it must carry HEAD): "+gitMessage(msg))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getWorkspaceBundle(w http.ResponseWriter, r *http.Request) {
	if !s.Backend.Capabilities()[api.CapWorkspaceBundle] {
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, "this endpoint cannot bundle a workspace out")
		return
	}
	q := r.URL.Query()
	base, branch := q.Get("base"), q.Get("branch")
	if !revRE.MatchString(base) && !validRef(base) {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "base: a commit id or branch name is required")
		return
	}
	if !validRef(branch) {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "branch: a git branch name is required")
		return
	}
	rec, ok := s.live(w, r)
	if !ok {
		return
	}
	id := rec.snapshot().ID
	ctx := r.Context()

	// Into a file first, so a failure is an honest status code rather than a
	// truncated 200.
	code, msg, err := s.execIn(ctx, id, []string{"git", "-C", "/workspace", "bundle", "create", "--quiet", bundleOut, base + ".." + branch}, nil, nil)
	if err != nil {
		writeBackendErr(w, err)
		return
	}
	if code != 0 {
		status, apiCode := http.StatusBadRequest, api.CodeInvalidRequest
		if strings.Contains(msg, "empty bundle") {
			status, apiCode = http.StatusConflict, api.CodeConflict // nothing new on the branch
		}
		writeErr(w, status, apiCode, "git bundle in the sandbox failed: "+gitMessage(msg))
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, _, _ = s.execIn(ctx, id, []string{"cat", bundleOut}, nil, &capWriter{w: w, left: maxBundle})
	_, _, _ = s.execIn(context.WithoutCancel(ctx), id, []string{"rm", "-f", bundleOut}, nil, nil)
}

// capWriter stops copying after left bytes: a guest's bundle is bounded too.
type capWriter struct {
	w    io.Writer
	left int64
}

func (c *capWriter) Write(p []byte) (int, error) {
	if c.left <= 0 {
		return len(p), nil
	}
	n := int64(len(p))
	if n > c.left {
		p = p[:c.left]
	}
	c.left -= int64(len(p))
	_, err := c.w.Write(p)
	return int(n), err
}
