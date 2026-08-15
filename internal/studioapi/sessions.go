package studioapi

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
)

// maxListedSessions bounds the picker. A long-lived agent HOME holds hundreds
// of conversations and the useful ones are the recent ones; the whole list is
// what `sandbox-cli context list` is for.
const maxListedSessions = 50

// maxSessionListLimit is as far as a caller may raise that. Reading every
// session's first lines is one open per file, so an unbounded listing is an
// unbounded amount of work on a machine with years of history.
const maxSessionListLimit = 500

// handleAgentSessions is GET /v1/agents/{agent}/sessions — conversations that
// can be resumed.
//
// Only the sandbox-owned store is listed, for the same reason the console reads
// only that one: those are the sessions a container can actually reopen. The
// user's own ~/.claude history is a real store and is not this daemon's to
// offer, since resuming it here would mean mounting the host's history into a
// container that was not asked to have it.
func (s *Server) handleAgentSessions(w http.ResponseWriter, r *http.Request) {
	agent := r.PathValue("agent")
	f, ok := agentctx.Resolve(agent, agentctx.DefaultRoots(), time.Now())
	if !ok || f.State != agentctx.StateVerified {
		// Not an error: an agent nobody has logged into has no conversations,
		// which an empty list says without inventing a reason.
		writeJSON(w, http.StatusOK, SessionListResponse{Sessions: []SessionSummary{}})
		return
	}
	// The registered repositories, so each conversation can say which one it
	// belongs to — read once for the whole listing rather than per session.
	projects := s.projects()

	// `scope=all` widens this to every verified store, for *reading*. The
	// default stays sandbox-only because it feeds the resume picker, where a
	// session that cannot be reopened is an action that fails — see the block
	// comment below on why reading and resuming are different questions.
	all := r.URL.Query().Get("scope") == "all"

	stores := []struct {
		f     agentctx.Finding
		store string
	}{{sandboxStore(f), storeSandbox}}
	if all {
		stores = append(stores, struct {
			f     agentctx.Finding
			store string
		}{f, storeHost})
	}

	seen := map[string]bool{}
	var out []SessionSummary
	for _, st := range stores {
		if st.f.Dir == "" {
			continue
		}
		found, _, err := agentctx.List(st.f, agentctx.ListOpts{})
		if err != nil {
			continue
		}
		for _, sess := range found {
			// A session present in both stores is reported once, as the
			// sandbox-owned copy — which is the one a container wrote and the one
			// that can be resumed.
			if seen[sess.ID] {
				continue
			}
			seen[sess.ID] = true
			summary := toSessionSummary(sess, st.store)
			summary.RepoID = repoForSession(sess, projects)
			out = append(out, summary)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	// The cap is what makes this a picker rather than an archive, but it is
	// recency-ordered — so a conversation from last week falls off it, and the
	// one somebody is looking for is often exactly that one. A caller that says
	// how many it wants gets that many, bounded.
	limit := maxListedSessions
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= maxSessionListLimit {
		limit = v
	}
	if len(out) > limit {
		out = out[:limit]
	}
	if out == nil {
		out = []SessionSummary{}
	}
	writeJSON(w, http.StatusOK, SessionListResponse{Sessions: out})
}

// Reading one conversation.
//
// The listing above is deliberately narrow — only the sandbox-owned store, only
// what a container can reopen — because it feeds a *resume* picker, and offering
// a session that cannot be resumed there would be offering an action that fails.
// Reading is a different question with a different answer: a conversation is
// worth looking at whether or not this daemon could reopen it, and the user's
// own ~/.claude history is where most of them are.
//
// So `?scope=all` widens the listing to every verified store, and every row says
// which store it came from and whether it can be resumed. That keeps the
// distinction the narrow default was protecting rather than erasing it — the
// launch form still asks for the default and still sees only resumable sessions.
//
// The rule that keeps this inside the boundary is the same one the repository
// registry and the file browser use: **a request names a session by id, never by
// path.** The daemon resolves the id against the stores agentctx has verified,
// so no request can ask this to open an arbitrary file, and the path is only
// ever *reported* — it is the answer to "where did this come from", which is the
// question a raw view exists to settle.

// maxRawTranscriptBytes bounds a raw read. A long conversation is genuinely
// megabytes — the one that prompted this is 4.5 MB — and a browser asked to
// render that as one string will stop being a browser. The tail is served
// rather than the head, because the end of a conversation is the part somebody
// is looking for, and the response says what it did.
const maxRawTranscriptBytes = 512 << 10

// findSession resolves a session id to the file holding it.
//
// Both stores are searched when host is true, and only the sandbox-owned one
// otherwise — the same asymmetry the listing draws, for the same reason. An id
// that matches nothing yields ok=false rather than a guess: there is no "closest
// session", and opening the wrong conversation is the failure this whole area
// is arranged to avoid.
func (s *Server) findSession(agent, id string, host bool) (sess agentctx.Session, store string, ok bool) {
	f, found := agentctx.Resolve(agent, agentctx.DefaultRoots(), time.Now())
	if !found || f.State != agentctx.StateVerified {
		return agentctx.Session{}, "", false
	}
	sandboxOnly := sandboxStore(f)

	// The sandbox-owned store first, so a session that exists in both is
	// reported as the one a container wrote — which is the one a run's view
	// means, and the one that can be resumed.
	for _, cand := range []struct {
		f     agentctx.Finding
		store string
	}{{sandboxOnly, storeSandbox}, {f, storeHost}} {
		if cand.f.Dir == "" {
			continue
		}
		if cand.store == storeHost && !host {
			continue
		}
		sessions, _, err := agentctx.List(cand.f, agentctx.ListOpts{})
		if err != nil {
			continue
		}
		for _, candidate := range sessions {
			if candidate.ID == id {
				return candidate, cand.store, true
			}
		}
	}
	return agentctx.Session{}, "", false
}

const (
	storeSandbox = "sandbox"
	storeHost    = "host"
)

// handleSessionTranscript is GET /v1/agents/{agent}/sessions/{id} — one
// conversation, parsed into turns.
func (s *Server) handleSessionTranscript(w http.ResponseWriter, r *http.Request) {
	agent, id := r.PathValue("agent"), r.PathValue("id")
	sess, store, ok := s.findSession(agent, id, true)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf(
			"no %s session %q in any store this daemon has verified", agent, id))
		return
	}
	limit := maxConversationTurns
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 5000 {
		limit = v
	}
	msgs, err := agentctx.Transcript(sess.Path, limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if msgs == nil {
		msgs = []agentctx.Message{}
	}
	writeJSON(w, http.StatusOK, SessionTranscriptResponse{
		Session:  toSessionSummary(sess, store),
		Messages: msgs,
	})
}

// handleSessionRaw is GET /v1/agents/{agent}/sessions/{id}/raw — the transcript
// file as it is on disk.
//
// It exists because a parsed view is an interpretation. sandbox-cli reads one
// format it has verified (claude's jsonl) and reports everything else as
// partial, so "show me what is actually in the file" is the only way to check
// the reading — and the only way to see the line kinds the parser drops on the
// floor, which for the file that prompted this is most of them: 550 attachments,
// 112 mode records, 120 queue operations, against 474 user turns.
func (s *Server) handleSessionRaw(w http.ResponseWriter, r *http.Request) {
	agent, id := r.PathValue("agent"), r.PathValue("id")
	sess, store, ok := s.findSession(agent, id, true)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf(
			"no %s session %q in any store this daemon has verified", agent, id))
		return
	}
	f, err := os.Open(sess.Path)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	resp := SessionRawResponse{Session: toSessionSummary(sess, store), Size: info.Size()}

	// The tail, not the head: a conversation is appended to, so the end is the
	// part somebody opening it is looking for. Said out loud, because a client
	// showing the last half of a file as though it were the file would be making
	// the same claim a truncated listing makes when it stays quiet.
	if info.Size() > maxRawTranscriptBytes {
		if _, err := f.Seek(info.Size()-maxRawTranscriptBytes, io.SeekStart); err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		resp.Truncated = true
	}
	buf, err := io.ReadAll(io.LimitReader(f, maxRawTranscriptBytes))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	// A tail starts mid-line. Dropping the partial first line means every line a
	// client is handed is parseable, rather than one that fails and looks like a
	// corrupt transcript.
	if resp.Truncated {
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	resp.Content = string(buf)
	writeJSON(w, http.StatusOK, resp)
}

func toSessionSummary(sess agentctx.Session, store string) SessionSummary {
	return SessionSummary{
		ID:       sess.ID,
		Title:    sess.Title,
		Turns:    sess.Turns,
		Modified: sess.Modified,
		Started:  sess.Started,
		Partial:  sess.Partial,
		Project:  sess.Project,
		Path:     sess.Path,
		Size:     sess.Size,
		Store:    store,
		// Only what a container can reopen. The host's own history is readable
		// here and is not this daemon's to resume: doing so would mean mounting
		// the host's history into a container that was not asked to have it.
		Resumable: store == storeSandbox,
	}
}

// repoForSession answers which repository a conversation belongs to.
//
// Two facts are available and neither is sufficient alone, which is why this is
// a function rather than a field read:
//
//   - The transcript records a **cwd**. For a host session that is the real
//     project path and settles it. For a *sandbox* session it is always
//     `/workspace`, because that is where every container mounts the project —
//     so it identifies the container's view and says nothing about which
//     repository was mounted there.
//   - The transcript's **path** carries the project bucket, because Claude Code
//     names that directory after its working directory and the claude wrapper
//     mounts the host's per-project bucket over the container's. So a synced
//     sandbox session lands in the bucket of the repository it worked on.
//
// The bucket is matched **forwards** — each registered project's root is
// converted to its bucket name and compared — rather than by decoding a bucket
// back into a path. Decoding is lossy: the mapping replaces every non-alphanumeric
// character with a dash, so `my-repo` and `my.repo` produce the same bucket and
// no reader can tell which it was.
//
// Empty means the conversation cannot be attributed, which is a real and common
// answer rather than a failure: a session pooled in the shared `-workspace`
// bucket records only `/workspace` and sits in a directory named for it, so
// nothing on disk says which repository it belonged to. Reporting "" lets a
// client hide those rather than file them under a repository they may not
// belong to.
func repoForSession(sess agentctx.Session, projects []Project) string {
	for _, p := range projects {
		if p.Root == "" {
			continue
		}
		for _, bucket := range agentctx.ProjectBuckets(p.Root) {
			if strings.Contains(sess.Path, string(filepath.Separator)+bucket+string(filepath.Separator)) {
				return p.ID
			}
		}
	}
	// A host session records the real directory it ran in, so it can be matched
	// the same way an audit line's workspace is.
	if sess.Project != "" && sess.Project != "/workspace" {
		return repoIDForWorkspace(sess.Project, projects)
	}
	return ""
}
