package studioapi

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/rescue"
	"github.com/Amitgb14/sandbox-cli/internal/s3"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// maxSnapshotsListed bounds a listing. A repository accumulates a session per
// run, so an old one has thousands; the bound is reported rather than applied
// silently, because a listing that stops without saying so reads as "this is
// everything".
const maxSnapshotsListed = 500

// callerSource decides which surface a request came from, and with it which
// surface may later restore what it creates.
//
// The mechanism is one guard.go already relies on: a browser attaches Origin to
// every cross-origin request — which is why originAllowed can refuse a page
// outright — and a programmatic client (the SDK, curl, a test) sends none. By
// the time a handler runs, an Origin that is present has already been checked
// against the allowed set, so its presence means "a browser we answer to", which
// in this daemon means Studio.
//
// **This is a scoping rule and not a security boundary, and it must not be
// mistaken for one.** Anything able to omit a header can restore anything; the
// bearer token and the loopback binding are what actually govern who may call
// this API at all. What the split buys is that the two surfaces do not silently
// undo each other's work — a scripted pipeline's checkpoints are not restorable
// by somebody clicking in a tab who has no idea what the script was doing
// halfway through.
func callerSource(r *http.Request) SnapshotSource {
	if r.Header.Get("Origin") != "" {
		return SnapshotSourceRun
	}
	return SnapshotSourceSDK
}

// effectiveSource reads a session's recorded source, treating the empty value as
// SnapshotSourceRun.
//
// Empty is not unknown here: every session predating the field was recorded by a
// sandbox run, so reading it as anything else would retroactively hide a user's
// existing snapshots from the only screen that lists them.
func effectiveSource(sess rescue.Session) SnapshotSource {
	if sess.Source == rescue.SourceSDK {
		return SnapshotSourceSDK
	}
	return SnapshotSourceRun
}

// retentionDefaults is the two windows in force for this daemon.
func (s *Server) retentionDefaults() rescue.Retention {
	spec := config.SnapshotSpec{}
	if s.Session != nil {
		spec = s.Session.Cfg.Snapshot
	}
	return rescue.Retention{
		Run:    spec.RetentionDuration(),
		Manual: spec.ManualRetentionDuration(),
	}
}

// toSnapshotInfo renders one snapshot for the wire.
func (s *Server) toSnapshotInfo(snap rescue.Snapshot, repoID string) SnapshotInfo {
	info := SnapshotInfo{
		ID:        snap.ID,
		RepoID:    repoID,
		Branch:    snap.Branch,
		Agent:     snap.Agent,
		Label:     snap.Label,
		Source:    effectiveSource(snap.Session),
		Commit:    snap.Commit,
		Reachable: snap.Reachable,
		CreatedAt: snap.StartedAt,
		EndedAt:   snap.EndedAt,
		Status:    snap.Status(),
		Retention: snap.Retention,
	}
	if d := s.retentionDefaults().For(snap.Session); d > 0 {
		info.RetentionEffective = d.String()
	}
	if r := snap.Remote; r != nil {
		info.Remote = &SnapshotRemote{
			Bucket:   r.Bucket,
			Key:      r.Key,
			Uploaded: r.Uploaded(),
			Bytes:    r.Bytes,
			Error:    r.Error,
		}
		if !r.UploadedAt.IsZero() {
			at := r.UploadedAt
			info.Remote.UploadedAt = &at
		}
	}
	return info
}

// snapshotS3 is the bucket configuration in force for this daemon, or nil.
func (s *Server) snapshotS3() *config.S3Spec {
	if s.Session == nil {
		return nil
	}
	return s.Session.Cfg.Snapshot.S3
}

// handleListSnapshots is GET /v1/snapshots?repo=&branch=.
//
// `repo=all` is the union across every repository, the same third spelling
// handleListWorktrees uses and for the same reason: the absent parameter means
// "the repository this daemon was started in", so "All repositories" in a UI
// cannot be spelled by leaving it out. Snapshots are per-repository on disk
// exactly as worktrees are — the manifests live under
// ~/.config/sandbox/rescue/<repo-id>/ — so the question is the same shape.
func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("repo") == "all"
	sc := s.defaultScope()
	if !all {
		var err error
		if sc, err = s.scopeOf(r); err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
	}
	snaps, err := rescue.List(sc.Project, all)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	branch := r.URL.Query().Get("branch")

	out := make([]SnapshotInfo, 0, len(snaps))
	for _, snap := range snaps {
		if branch != "" && snap.Branch != branch {
			continue
		}
		// A baseline is a before-image the daemon takes at launch, not a
		// recovery point. Listing it beside real snapshots would offer to
		// restore a run's starting state as though it were its work.
		if snap.Outcome == baselineOutcome {
			continue
		}
		repoID := sc.RepoID
		if all {
			// Across repositories the scope's id would label every row with the
			// default repository's, which is worse than an empty one: a client
			// would file another project's snapshot under this one.
			repoID = s.repoIDOf(snap.Repo)
		}
		out = append(out, s.toSnapshotInfo(snap, repoID))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })

	resp := SnapshotListResponse{Snapshots: out}
	if len(resp.Snapshots) > maxSnapshotsListed {
		resp.Snapshots = resp.Snapshots[:maxSnapshotsListed]
		resp.Truncated = true
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleCreateSnapshot is POST /v1/snapshots: capture a workspace now.
//
// The workspace is the *branch's worktree*, resolved by the same helper
// /v1/files browses through. Capturing the repository root while an agent works
// in a worktree would snapshot the wrong tree, plausibly and without saying so.
func (s *Server) handleCreateSnapshot(w http.ResponseWriter, r *http.Request) {
	var req SnapshotCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sc, err := s.scopeFor(req.Repo)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	root, err := s.browseRoot(sc, req.Branch)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	s.captureInto(w, r, sc, root, "", req.Label, req.Retention)
}

// handleSnapshotRun is POST /v1/runs/{id}/snapshot: capture the workspace a run
// is working in, without the caller having to know which worktree that is.
func (s *Server) handleSnapshotRun(w http.ResponseWriter, r *http.Request) {
	c, err := s.resolveRun(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	var req SnapshotCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Repo != "" || req.Branch != "" {
		// The run already answers both, from the labels it was stamped with.
		// Accepting a second answer is how files get written under a repository
		// the run never touched.
		writeError(w, http.StatusBadRequest, errors.New(
			"repo and branch are taken from the run itself; remove them from the body"))
		return
	}
	sc := s.scopeOfRun(c.Labels[sandbox.LabelRepo])
	root, err := s.browseRoot(sc, c.Labels[sandbox.LabelBranch])
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	s.captureInto(w, r, sc, root, c.Labels[sandbox.LabelAgent], req.Label, req.Retention)
}

// captureInto is the shared body of the two create endpoints.
func (s *Server) captureInto(w http.ResponseWriter, r *http.Request, sc repoScope, workspace, agent, label, retention string) {
	if retention != "" {
		if d, err := time.ParseDuration(retention); err != nil || d <= 0 {
			writeError(w, http.StatusBadRequest, fmt.Errorf(
				"retention must be a positive Go duration (%q, %q), got %q", "72h", "168h", retention))
			return
		}
	}
	snap, err := rescue.Capture(workspace, rescue.CaptureOptions{
		Agent:     agent,
		Label:     label,
		Retention: retention,
		// Derived, never accepted from the body: a caller able to label its own
		// snapshots would be choosing which surface may later restore them.
		Source: string(callerSource(r)),
		// The bucket comes from this daemon's resolved configuration and never
		// from the request. A body that could name one would let any caller
		// choose where a repository's contents are sent, which is the same
		// reason trust.go refuses the key from a project file.
		S3: s.snapshotS3(),
	})
	switch {
	case errors.Is(err, rescue.ErrNothingToSnapshot):
		// 422 rather than an empty 200. A caller handed an id pointing at no
		// commit would believe it had a checkpoint it does not have.
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	case err != nil && snap.ID != "":
		// The snapshot was taken and the mirror failed. Reporting only the error
		// would lose the id of a checkpoint that exists — so this answers 201
		// with the snapshot, whose remote block carries the failure, and the
		// caller sees a checkpoint that is real and local-only. Which is the
		// truth, and is exactly what the listing will show tomorrow.
		writeJSON(w, http.StatusCreated, s.toSnapshotInfo(snap, sc.RepoID))
		return
	case err != nil:
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.toSnapshotInfo(snap, sc.RepoID))
}

// handleUploadSnapshot is POST /v1/snapshots/{id}/upload: mirror one snapshot to
// object storage now.
//
// It exists for the two cases the automatic path leaves behind — an upload that
// failed while the network was down, and a snapshot taken before a bucket was
// configured — and it is deliberately not a toggle: there is no "unmirror",
// because deleting somebody's backup is not a thing a UI should do by accident.
// DELETE is the pruning path and says so.
func (s *Server) handleUploadSnapshot(w http.ResponseWriter, r *http.Request) {
	spec := s.snapshotS3()
	if spec == nil || spec.Bucket == "" {
		writeError(w, http.StatusUnprocessableEntity, errors.New(
			"no snapshot bucket is configured; set snapshot.s3.bucket in Settings"))
		return
	}
	// An empty body is normal here — the repository is the only input, and the
	// default one is the common case — so a decode failure is not fatal.
	var req SnapshotRepoRequest
	_ = decodeJSON(r, &req)
	sc, err := s.scopeFor(req.Repo)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	snap, err := rescue.Find(sc.Project, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if !snap.Reachable {
		// There is nothing to bundle. Said here rather than letting `git bundle`
		// fail, because "objects are gone" and "the bucket refused us" are
		// different problems with different remedies and only one of them is
		// about S3.
		writeError(w, http.StatusUnprocessableEntity, fmt.Errorf(
			"snapshot %s has no objects left in the repository to upload", snap.ID))
		return
	}
	sess := snap.Session
	if err := rescue.Mirror(r.Context(), &sess, spec); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toSnapshotInfo(rescue.Snapshot{
		Session: sess, Commit: snap.Commit, Reachable: snap.Reachable,
	}, sc.RepoID))
}

// handleVerifySnapshot is POST /v1/snapshots/{id}/verify: ask the bucket whether
// the object is really there.
//
// The one call that asks rather than remembers — see SnapshotRemote. A lifecycle
// rule or somebody tidying a bucket leaves a manifest claiming a copy that is
// gone, and a backup nobody checks is a backup nobody has.
func (s *Server) handleVerifySnapshot(w http.ResponseWriter, r *http.Request) {
	spec := s.snapshotS3()
	if spec == nil || spec.Bucket == "" {
		writeError(w, http.StatusUnprocessableEntity, errors.New("no snapshot bucket is configured"))
		return
	}
	var req SnapshotRepoRequest
	_ = decodeJSON(r, &req)
	sc, err := s.scopeFor(req.Repo)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	snap, err := rescue.Find(sc.Project, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	size, err := rescue.Verify(r.Context(), snap.Session, spec)
	resp := SnapshotS3CheckResponse{Bucket: spec.Bucket, Endpoint: spec.Endpoint}
	if err != nil {
		resp.Error = err.Error()
	} else {
		resp.Ok = size >= 0
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleCheckS3 is POST /v1/snapshots/s3/check: does the configured bucket
// answer, and do the named credentials resolve.
//
// It checks what is **configured on this daemon**, not what is in the request
// body. A check that took a bucket and an endpoint from the caller would be a
// server-side request forgery with a Test button in front of it — the daemon
// signing a request to any host somebody names, and reporting whether it
// answered.
func (s *Server) handleCheckS3(w http.ResponseWriter, r *http.Request) {
	spec := s.snapshotS3()
	if spec == nil || spec.Bucket == "" {
		writeJSON(w, http.StatusOK, SnapshotS3CheckResponse{
			Error: "no bucket is configured",
		})
		return
	}
	resp := SnapshotS3CheckResponse{Bucket: spec.Bucket, Endpoint: spec.Endpoint}
	if err := rescue.CheckRemote(r.Context(), spec); err != nil {
		resp.Error = err.Error()
	} else {
		resp.Ok = true
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleRestoreSnapshot is POST /v1/snapshots/{id}/restore.
func (s *Server) handleRestoreSnapshot(w http.ResponseWriter, r *http.Request) {
	var req SnapshotRestoreRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sc, err := s.scopeFor(req.Repo)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	snap, err := rescue.Find(sc.Project, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if from := callerSource(r); from == SnapshotSourceRun && effectiveSource(snap.Session) == SnapshotSourceSDK {
		writeError(w, http.StatusForbidden, fmt.Errorf(
			"snapshot %s was taken through the SDK and is restored through the SDK; "+
				"a program is mid-way through something this screen cannot see", snap.ID))
		return
	}

	// The objects are gone locally but there is a copy in the bucket: fetch it
	// back into refs/sandbox/ and carry on. This is the whole reason mirroring is
	// a backup rather than an offload — restore reads a local ref, so bringing a
	// snapshot home means putting the objects where they always were, and none of
	// the three restore modes has to learn that a network exists.
	//
	// Attempted only when the local copy is missing. Fetching one that is already
	// here would spend a download to arrive at the bytes already on disk.
	if !snap.Reachable && snap.Remote.Uploaded() {
		sess := snap.Session
		if err := rescue.Fetch(r.Context(), &sess, s.snapshotS3()); err != nil {
			writeError(w, http.StatusUnprocessableEntity, fmt.Errorf(
				"snapshot %s is not in this repository and could not be fetched from %s: %w",
				snap.ID, sess.Remote.Bucket, err))
			return
		}
	}

	resp, status, err := s.restoreSession(snap.Session, req.Mode, req.Branch)
	if err != nil {
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleSnapshotRetention is POST /v1/snapshots/{id}/retention.
func (s *Server) handleSnapshotRetention(w http.ResponseWriter, r *http.Request) {
	var req SnapshotRetentionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sc, err := s.scopeFor(req.Repo)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	snap, err := rescue.SetRetention(sc.Project, r.PathValue("id"), req.Retention)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toSnapshotInfo(snap, sc.RepoID))
}

// handleGetSnapshotSettings is GET /v1/snapshots/settings.
func (s *Server) handleGetSnapshotSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.snapshotSettings())
}

// handleSetSnapshotSettings is POST /v1/snapshots/settings.
//
// A narrow endpoint, not a config writer — the argument handleSetProviders makes
// at length and this inherits: it writes two duration strings into a file of its
// own, and cannot turn snapshotting off or change its cadence. `enabled: false`
// silently removes crash protection and a millisecond interval turns the host
// into a sustained `git add -A` loop, which is why trust.go refuses the whole
// `snapshot` key from a project file. A UI is not a project file, but it is not a
// reason to reopen the question either.
func (s *Server) handleSetSnapshotSettings(w http.ResponseWriter, r *http.Request) {
	var req SnapshotSettings
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	save := config.SnapshotSpec{
		Retention:       req.Retention,
		ManualRetention: req.ManualRetention,
	}
	// The bucket is written only when config.yaml does not already set one. The
	// alternative — accepting the write and having it silently outranked at the
	// next restart — is the failure the ConfigManaged flag exists to prevent, and
	// refusing it here is what makes that flag a promise rather than a hint.
	managed := config.SnapshotOverrides()
	configS3 := s.configS3(managed)
	if req.S3 != nil && configS3 {
		writeError(w, http.StatusConflict, errors.New(
			"snapshot.s3 is set in config.yaml, which outranks this screen; edit it there"))
		return
	}
	switch {
	case req.S3 != nil:
		save.S3 = &config.S3Spec{
			Bucket:          strings.TrimSpace(req.S3.Bucket),
			Region:          strings.TrimSpace(req.S3.Region),
			Endpoint:        strings.TrimSpace(req.S3.Endpoint),
			Prefix:          strings.TrimSpace(req.S3.Prefix),
			PathStyle:       req.S3.PathStyle,
			Upload:          req.S3.Upload,
			AccessKeyEnv:    strings.TrimSpace(req.S3.AccessKeyEnv),
			SecretKeyEnv:    strings.TrimSpace(req.S3.SecretKeyEnv),
			SessionTokenEnv: strings.TrimSpace(req.S3.SessionTokenEnv),
			MaxObjectMB:     req.S3.MaxObjectMB,
		}
	case !configS3:
		// Absent means "leave it alone", not "turn it off": a client that only
		// meant to change a retention window must not clear somebody's bucket by
		// omitting a field it does not know about. Turning it off is spelled by
		// sending an s3 block with an empty bucket.
		save.S3 = managed.S3
	}
	if err := config.SaveSnapshotOverrides(save); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	// Applied to the running daemon too, so a later listing reports the window
	// that was just chosen rather than the one this process started with — and so
	// the next capture mirrors to the bucket just configured rather than needing
	// a restart to notice it.
	if s.Session != nil {
		s.Session.Cfg.Snapshot.Retention = req.Retention
		s.Session.Cfg.Snapshot.ManualRetention = req.ManualRetention
		if !configS3 {
			s.Session.Cfg.Snapshot.S3 = config.SnapshotOverrides().S3
		}
	}
	writeJSON(w, http.StatusOK, s.snapshotSettings())
}

// configS3 reports that config.yaml sets the bucket, as opposed to this daemon's
// own override file.
//
// Read the way snapshotSettings reads the retention windows: what is resolved,
// minus this daemon's own layer. Anything still set after the override is
// discounted was typed by hand, and this screen does not get to overwrite it.
func (s *Server) configS3(managed config.SnapshotSpec) bool {
	if s.Session == nil || s.Session.Cfg.Snapshot.S3 == nil {
		return false
	}
	if managed.S3 == nil {
		return true
	}
	// Same bucket from both layers means the override is what put it there;
	// comparing the bucket alone is enough, since the override file is written
	// whole and a differing bucket can only have come from config.yaml.
	return managed.S3.Bucket != s.Session.Cfg.Snapshot.S3.Bucket
}

// snapshotSettings reports both the value in force and where it came from. Those
// are different questions, and a screen that writes one of them needs both: this
// daemon's own file is a layer *under* config.yaml, so a value typed by hand
// outranks one set here and an edit to it would not survive a restart.
func (s *Server) snapshotSettings() SnapshotSettings {
	d := s.retentionDefaults()
	out := SnapshotSettings{
		Retention:       d.Run.String(),
		ManualRetention: d.Manual.String(),
		Writable:        config.SnapshotOverridesPath() != "",
	}
	// What config.yaml sets is the resolved value minus this daemon's own layer:
	// anything still set after the override is removed was typed by hand.
	managed := config.SnapshotOverrides()
	if s.Session != nil {
		if v := s.Session.Cfg.Snapshot.Retention; v != "" && v != managed.Retention {
			out.ConfigRetention = v
		}
		if v := s.Session.Cfg.Snapshot.ManualRetention; v != "" && v != managed.ManualRetention {
			out.ConfigManualRetention = v
		}
	}
	if spec := s.snapshotS3(); spec != nil {
		out.S3 = &SnapshotS3Settings{
			Bucket:          spec.Bucket,
			Region:          spec.Region,
			Endpoint:        spec.Endpoint,
			Prefix:          spec.Prefix,
			PathStyle:       spec.PathStyle,
			Upload:          spec.UploadMode(),
			AccessKeyEnv:    spec.AccessKeyEnv,
			SecretKeyEnv:    spec.SecretKeyEnv,
			SessionTokenEnv: spec.SessionTokenEnv,
			MaxObjectMB:     spec.MaxObjectMB,
			ConfigManaged:   s.configS3(managed),
		}
		// Asked of the environment rather than assumed. The values themselves
		// never leave this function: what crosses the wire is a boolean and, when
		// it is false, a sentence naming the variable to set — which is the whole
		// of what a screen needs and the most it may have.
		names := s3.Config{
			AccessKeyEnv:    spec.AccessKeyEnv,
			SecretKeyEnv:    spec.SecretKeyEnv,
			SessionTokenEnv: spec.SessionTokenEnv,
		}
		if _, err := names.Credentials(); err != nil {
			out.S3.CredentialsError = err.Error()
		} else {
			out.S3.CredentialsResolved = true
		}
	}
	return out
}

// restoreSession performs a restore and renders the response, shared by
// /v1/snapshots/{id}/restore and /v1/runs/{id}/recover so the two cannot drift
// on what a mode means or on how a patch is returned.
//
// Returns the HTTP status to use for a failure, because the reasons are not all
// the same kind: an unknown mode is the caller's mistake, while a dirty worktree
// is a state the request was right to ask about and wrong to assume.
func (s *Server) restoreSession(sess rescue.Session, mode RestoreMode, branch string) (RunRecoverResponse, int, error) {
	if mode == "" {
		mode = RestoreModeBranch
	}
	opts := rescue.RestoreOptions{Branch: branch}
	var patchFile string
	switch mode {
	case RestoreModeBranch:
		opts.Mode = rescue.RestoreBranch
	case RestoreModeWorktree:
		opts.Mode = rescue.RestoreWorktree
	case RestoreModePatch:
		opts.Mode = rescue.RestorePatch
		// Restore writes the patch to the process's own stdout when Out is ""/"-",
		// which is the server's terminal, not the HTTP response. A temp file lets
		// this API return the text instead of printing it somewhere the caller
		// cannot see.
		f, err := os.CreateTemp("", "sandbox-studio-restore-*.patch")
		if err != nil {
			return RunRecoverResponse{}, http.StatusInternalServerError, err
		}
		patchFile = f.Name()
		f.Close()
		opts.Out = patchFile
		defer os.Remove(patchFile)
	default:
		return RunRecoverResponse{}, http.StatusBadRequest, fmt.Errorf(
			"mode must be %q, %q, or %q", RestoreModeBranch, RestoreModePatch, RestoreModeWorktree)
	}

	result, err := rescue.Restore(sess.Workspace, sess.ID, opts)
	if err != nil {
		return RunRecoverResponse{}, http.StatusUnprocessableEntity, err
	}
	resp := RunRecoverResponse{
		SessionID:          sess.ID,
		Mode:               mode,
		Branch:             result.Branch,
		Files:              result.Files,
		MatchesWorkingTree: result.MatchesWorkingTree,
	}
	if patchFile != "" {
		if b, err := os.ReadFile(patchFile); err == nil {
			resp.Patch = string(b)
		}
	}
	return resp, http.StatusOK, nil
}

// repoIDOf maps a repository root back to the id this daemon knows it by, for
// the listing that spans repositories. Empty when nothing registered matches:
// a snapshot recorded for a project since removed is still real, and labelling
// it with a repository it does not belong to would be worse than saying nothing.
func (s *Server) repoIDOf(root string) string {
	if root == s.Project {
		return s.RepoID
	}
	for _, p := range s.projects() {
		if p.Root == root {
			return p.ID
		}
	}
	return ""
}
