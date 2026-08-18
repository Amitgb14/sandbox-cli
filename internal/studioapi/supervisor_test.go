package studioapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/audit"
	"github.com/Amitgb14/sandbox-cli/internal/handoff"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// A supervised run, wound to the point where its container has just exited.
//
// tree is what the workspace fingerprints to at every reading, so the default is
// a run that changed nothing — the case a failover exists for. A test that wants
// the opposite moves it between the launch and the exit.
func supervised(t *testing.T, exit int, tree string) (*Server, *fakeRuntime, *supervisor, *watch) {
	t.Helper()
	s, fr := newTestServer(t)
	sv := s.sv()
	sv.treeOf = func(string) (string, error) { return tree, nil }

	name := "sandbox-testrepo-feat-x"
	fr.containers = append(fr.containers, runtime.ContainerInfo{
		ID:     "c1",
		Name:   name,
		Labels: map[string]string{sandbox.LabelCLI: "1"},
		State:  "running",
	})
	w := &watch{
		container: "c1",
		name:      name,
		req:       RunCreateRequest{Agent: "claude", Prompt: "fix the parser", Fallback: []string{"codex"}},
		agent:     "claude",
		remaining: []string{"codex"},
		workspace: s.Project,
		before:    "tree-before",
		routeID:   "20260815-120000-abcdef",
		attempt:   1,
	}
	sv.supervise(w)
	fr.setState("c1", "exited")
	for i := range fr.containers {
		if fr.containers[i].ID == "c1" {
			fr.containers[i].ExitCode = exit
		}
	}
	return s, fr, sv, w
}

// The rule the whole feature turns on: a run that failed having written nothing
// is an outage, and the next agent gets the work.
func TestSupervisorStartsTheNextAgentWhenNothingWasWritten(t *testing.T) {
	_, fr, sv, _ := supervised(t, 1, "tree-before")

	sv.tick(context.Background())

	if len(fr.started) != 1 {
		t.Fatalf("started %d containers, want the fallback to have been launched", len(fr.started))
	}
	spec := fr.started[0]
	if got := spec.Labels[sandbox.LabelAgent]; got != "codex" {
		t.Errorf("the retry ran %q, want codex", got)
	}
	// One episode, two attempts. Without a shared id the two rows are two
	// unrelated runs and "did routing help" cannot be asked of the log.
	if got := spec.Labels[sandbox.LabelRouteID]; got != "20260815-120000-abcdef" {
		t.Errorf("route id = %q, want the episode's", got)
	}
	if got := spec.Labels[sandbox.LabelRoutedFrom]; got != "claude" {
		t.Errorf("routed_from = %q, want claude", got)
	}

	// This run is on no branch, so its name is a timestamp that collides with
	// nothing and the failed container is left exactly where it is. The other
	// case — the deterministic name that enforces one-agent-per-branch — is
	// TestTheRetryTakesBackTheNameThatEnforcesOneAgentPerBranch below.
	if len(fr.renamed) != 0 {
		t.Errorf("renamed %v, want nothing renamed for a run whose name collides with nothing", fr.renamed)
	}

	// And it is supervised in turn — a chain of three must be able to fail twice.
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if _, still := sv.watched["c1"]; still {
		t.Error("the finished container is still being watched")
	}
	if len(sv.watched) != 1 {
		t.Fatalf("watching %d runs, want the retry", len(sv.watched))
	}
	for _, w := range sv.watched {
		if w.attempt != 2 {
			t.Errorf("retry recorded as attempt %d, want 2", w.attempt)
		}
		if w.routeID != "20260815-120000-abcdef" {
			t.Errorf("retry route id = %q, want the episode's", w.routeID)
		}
		if len(w.remaining) != 0 {
			t.Errorf("remaining = %v, want the chain exhausted", w.remaining)
		}
	}
}

// The gate, from the other side. A run that wrote files failed at the task
// rather than at the provider, and a second agent inheriting half-finished edits
// is the outcome this must never produce.
func TestSupervisorLeavesARunThatWroteSomething(t *testing.T) {
	_, fr, sv, _ := supervised(t, 1, "tree-after")

	sv.tick(context.Background())

	if len(fr.started) != 0 {
		t.Errorf("started %d containers, want none — the workspace changed", len(fr.started))
	}
	if len(fr.renamed) != 0 {
		t.Errorf("renamed %v, want the failed container left as it is", fr.renamed)
	}
	if len(sv.watched) != 0 {
		t.Error("the run is still watched after it settled")
	}
}

// A workspace that cannot be read is not evidence of an idle run. Unknowable
// counts as work done, because a wrong retry destroys work and a missed one
// costs a re-run somebody asks for by hand.
func TestSupervisorDoesNotRetryWhatItCannotMeasure(t *testing.T) {
	_, fr, sv, _ := supervised(t, 1, "")
	sv.treeOf = func(string) (string, error) { return "", errors.New("not a repository") }

	sv.tick(context.Background())

	if len(fr.started) != 0 {
		t.Errorf("started %d containers, want none — the workspace could not be compared", len(fr.started))
	}
	if len(sv.watched) != 0 {
		t.Error("the run is still watched after it settled")
	}
}

func TestSupervisorLeavesASuccessfulRunAlone(t *testing.T) {
	_, fr, sv, _ := supervised(t, 0, "tree-before")

	sv.tick(context.Background())

	if len(fr.started) != 0 {
		t.Errorf("started %d containers after an exit 0", len(fr.started))
	}
	if len(sv.watched) != 0 {
		t.Error("a finished run is still watched")
	}
}

// A container that has gone — reaped by `clean`, or by a person — has no exit
// code, so there is nothing to decide and nothing to retry.
func TestSupervisorDropsARunThatVanished(t *testing.T) {
	_, fr, sv, _ := supervised(t, 1, "tree-before")
	fr.containers = nil

	sv.tick(context.Background())

	if len(fr.started) != 0 {
		t.Errorf("started %d containers for a container that no longer exists", len(fr.started))
	}
	if len(sv.watched) != 0 {
		t.Error("a vanished run is still watched")
	}
}

// The engine being unreachable is not a decision. Runs stay watched, so a daemon
// that comes back finds them supervised rather than abandoned.
func TestSupervisorKeepsWatchingWhenTheEngineCannotBeAsked(t *testing.T) {
	s, fr, sv, _ := supervised(t, 1, "tree-before")
	s.RT = &unreachableRuntime{fakeRuntime: fr}

	sv.tick(context.Background())

	if len(sv.watched) != 1 {
		t.Errorf("watching %d runs, want the run kept while the engine is unreachable", len(sv.watched))
	}
}

type unreachableRuntime struct {
	*fakeRuntime
}

func (u *unreachableRuntime) Containers(context.Context, map[string]string) ([]runtime.ContainerInfo, error) {
	return nil, errors.New("cannot connect to the docker daemon")
}

// The briefing is mounted where its prompt says it is. The prompt tells the next
// agent to read /sandbox/context; an agent handed no such mount burns its turns
// looking for a directory that does not exist — the same silent failure the CLI
// side shipped once already.
func TestRetryCarriesTheBriefing(t *testing.T) {
	_, fr, sv, _ := supervised(t, 1, "tree-before")

	sv.tick(context.Background())

	if len(fr.started) != 1 {
		t.Fatalf("started %d containers, want the fallback", len(fr.started))
	}
	var mounted string
	for _, m := range fr.started[0].Mounts {
		if m.Target == handoff.GuestDir {
			mounted = m.Source
		}
	}
	if mounted == "" {
		t.Fatalf("no mount at %s: %v", handoff.GuestDir, fr.started[0].Mounts)
	}
	// And the prompt says so, in the words that make it a briefing rather than a
	// resumed conversation.
	if got := fr.started[0].Labels[sandbox.LabelPrompt]; !strings.Contains(got, handoff.GuestDir) {
		t.Errorf("the retry's prompt does not point at the briefing: %q", got)
	}
}

func TestRemainingAfter(t *testing.T) {
	chain := []string{"claude", "codex", "gemini"}
	for _, tc := range []struct {
		agent string
		want  []string
	}{
		{"claude", []string{"codex", "gemini"}},
		{"codex", []string{"gemini"}},
		{"gemini", nil},
		// Not in the chain at all: nothing follows an agent that is not there,
		// which is the honest answer rather than the whole chain.
		{"droid", nil},
	} {
		got := remainingAfter(chain, tc.agent)
		if len(got) != len(tc.want) {
			t.Errorf("remainingAfter(%q) = %v, want %v", tc.agent, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("remainingAfter(%q) = %v, want %v", tc.agent, got, tc.want)
				break
			}
		}
	}
}

// The name is the lock. A detached run in a repository and on a branch is named
// sandbox-<repo>-<branch>, and docker's atomic refusal of a duplicate is what
// stops two agents working in one checkout — so a retry has to hold that name,
// and the failed attempt has to give it up without being deleted, since its logs
// are why the failover happened.
func TestTheRetryTakesBackTheNameThatEnforcesOneAgentPerBranch(t *testing.T) {
	s, fr := newTestServer(t)
	sv := s.sv()
	held := sandbox.ContainerName(sandbox.Options{Detach: true, RepoID: "testrepo", Branch: "feat/x"})
	fr.containers = append(fr.containers, runtime.ContainerInfo{ID: "c1", Name: held})
	w := &watch{container: "c1", name: held, attempt: 1}

	restore := sv.handOverName(context.Background(),
		w, sandbox.Options{Detach: true, RepoID: "testrepo", Branch: "feat/x"})

	if len(fr.renamed) != 1 || fr.renamed[0][0] != "c1" {
		t.Fatalf("renames = %v, want the failed container to have given up the name", fr.renamed)
	}
	if want := held + "-attempt1"; fr.renamed[0][1] != want {
		t.Errorf("renamed to %q, want %q", fr.renamed[0][1], want)
	}
	if len(fr.removed) != 0 {
		t.Errorf("removed %v — the failed container carries the logs that explain the failover", fr.removed)
	}

	// And a launch that never happens puts it back. A container left under a
	// name saying it was superseded, when nothing superseded it, is a false
	// record that outlives the incident.
	restore()
	if len(fr.renamed) != 2 || fr.renamed[1][1] != held {
		t.Errorf("renames = %v, want the original name restored", fr.renamed)
	}
}

// A run with no branch is named for the clock, collides with nothing, and is
// left alone: renaming it would move a row somebody is reading to solve a
// problem it does not have.
func TestARunWithNoBranchIsNotRenamed(t *testing.T) {
	s, fr := newTestServer(t)
	sv := s.sv()
	fr.containers = append(fr.containers, runtime.ContainerInfo{ID: "c1", Name: "sandbox-abc123"})
	w := &watch{container: "c1", name: "sandbox-abc123", attempt: 1}

	sv.handOverName(context.Background(), w, sandbox.Options{Detach: true, RepoID: "testrepo"})

	if len(fr.renamed) != 0 {
		t.Errorf("renamed %v, want nothing touched", fr.renamed)
	}
}

// The handler's half of it: a launch with somewhere to fall through to is
// watched, and the episode it belongs to is stamped from the first attempt.
//
// The route id has to be minted here rather than at the first switch. A failover
// writes two audit lines, and without a shared id on *both* they are two
// unrelated runs — so the question routing exists to answer, did it rescue this
// run or waste a container, is unanswerable for exactly the runs it rescued.
func TestALaunchWithAFallbackIsSupervised(t *testing.T) {
	s, fr := newTestServer(t)
	// No probing: an explicit empty provider host means "nothing to ask", which
	// keeps a unit test off the network. Otherwise routeAgent would HEAD
	// api.anthropic.com from a test run.
	s.Session.Cfg.Providers = map[string]string{"claude": "", "codex": ""}

	rec := doJSON(t, s, http.MethodPost, "/v1/runs", RunCreateRequest{
		Agent: "claude", Prompt: "fix the parser", Fallback: []string{"codex"},
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/runs = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if len(fr.started) != 1 {
		t.Fatalf("started %d containers, want 1", len(fr.started))
	}
	if id := fr.started[0].Labels[sandbox.LabelRouteID]; id == "" {
		t.Error("no route id on the first attempt: a failover's two rows would not be connectable")
	}

	sv := s.sv()
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if len(sv.watched) != 1 {
		t.Fatalf("watching %d runs, want the launch to be supervised", len(sv.watched))
	}
	for _, w := range sv.watched {
		if w.agent != "claude" {
			t.Errorf("watching agent %q, want claude", w.agent)
		}
		if len(w.remaining) != 1 || w.remaining[0] != "codex" {
			t.Errorf("remaining = %v, want [codex]", w.remaining)
		}
		if w.attempt != 1 {
			t.Errorf("attempt = %d, want 1", w.attempt)
		}
		if w.req.Prompt != "fix the parser" {
			t.Errorf("the watch holds prompt %q, want the one the user typed", w.req.Prompt)
		}
	}
}

// A launch with nowhere to fall through to is watched too, and that is a change
// of mind worth stating: it used to be skipped, on the grounds that a supervisor
// polling a run it can never retry is paying for a decision it cannot make.
//
// The decision was not the only job. A detached run's audit line is written at
// launch — there is no exit code to wait for — so without something watching,
// "did it pass" was answered by a placeholder 0 for every run Studio started.
// Recording the ending needs no chain, only a container that will stop.
func TestALaunchWithNoFallbackIsStillWatchedForItsEnding(t *testing.T) {
	s, _ := newTestServer(t)
	rec := doJSON(t, s, http.MethodPost, "/v1/runs", RunCreateRequest{
		Agent: "claude", Prompt: "fix the parser",
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/runs = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	sv := s.sv()
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if len(sv.watched) != 1 {
		t.Fatalf("watching %d runs, want the launch watched so its ending is recorded", len(sv.watched))
	}
	for _, w := range sv.watched {
		if len(w.remaining) != 0 {
			t.Errorf("remaining = %v, want nothing to fall through to", w.remaining)
		}
		// The record it will complete when the container stops.
		if w.meta.RunID == "" {
			t.Error("no launch record captured, so the ending would have nothing to be matched to")
		}
		if w.meta.Finished {
			t.Error("the launch record claims to be finished")
		}
	}
}

// What a listing can say about a failover *while it is happening*.
//
// The audit log answers this afterwards, and only afterwards: a detached run's
// line is written when it ends. So everything the Runs screen needs to show two
// containers as one episode has to be on the containers — which it was not. The
// wire type had `routeId` and `routeAttempt`; toRun never filled them, and
// `sandbox.route_attempt` was not stamped at all, so the ordering within an
// episode existed nowhere a live screen could read it. Found by running a real
// failover and looking at the labels.
func TestARunCarriesItsEpisodeInItsLabels(t *testing.T) {
	_, fr, sv, _ := supervised(t, 1, "tree-before")

	sv.tick(context.Background())

	if len(fr.started) != 1 {
		t.Fatalf("started %d containers, want the fallback", len(fr.started))
	}
	if got := fr.started[0].Labels[sandbox.LabelRouteAttempt]; got != "2" {
		t.Errorf("route_attempt label = %q, want \"2\"", got)
	}

	// And it survives the trip back out through the wire type, which is where
	// the first version lost it.
	run := toRun(runtime.ContainerInfo{
		ID: "abc", Labels: fr.started[0].Labels, State: "running",
	}, "docker")
	if run.RouteAttempt != 2 {
		t.Errorf("Run.routeAttempt = %d, want 2", run.RouteAttempt)
	}
	if run.RouteID != "20260815-120000-abcdef" {
		t.Errorf("Run.routeId = %q, want the episode's", run.RouteID)
	}
	if run.RoutedFrom != "claude" {
		t.Errorf("Run.routedFrom = %q, want claude", run.RoutedFrom)
	}
}

// An ordinary run carries no routing labels at all. "Attempt 1 of 1" is a fact
// about a run that could never route, and stamping it would put a routing column
// on every container in the listing.
func TestAnUnroutableRunCarriesNoEpisode(t *testing.T) {
	s, fr := newTestServer(t)
	rec := doJSON(t, s, http.MethodPost, "/v1/runs", RunCreateRequest{
		Agent: "claude", Prompt: "fix the parser",
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/runs = %d: %s", rec.Code, rec.Body.String())
	}
	for _, label := range []string{sandbox.LabelRouteID, sandbox.LabelRouteAttempt, sandbox.LabelRoutedFrom} {
		if got, ok := fr.started[0].Labels[label]; ok {
			t.Errorf("%s = %q on a run with no chain, want it absent", label, got)
		}
	}
}

// The line a detached run cannot write for itself.
//
// Its launch line carries Finished=false and a placeholder exit code, because at
// that moment there is nothing else true to say. When the container stops, the
// supervisor writes the partner: same RunID, real exit code, and a duration
// measured from the container's own timestamps rather than from this process's
// clock — the daemon may have started after the run did.
func TestTheSupervisorRecordsHowARunEnded(t *testing.T) {
	s, fr := newTestServer(t)
	rec := &recordingSink{}
	s.Session.Audit = rec
	sv := s.sv()
	sv.treeOf = func(string) (string, error) { return "", errors.New("no repo") }

	started := time.Now().Add(-3 * time.Minute)
	fr.containers = append(fr.containers, runtime.ContainerInfo{
		ID: "c9", Name: "sandbox-x", Labels: map[string]string{sandbox.LabelCLI: "1"},
		State: "exited", ExitCode: 7,
		StartedAt: started, FinishedAt: started.Add(2 * time.Minute),
	})
	sv.supervise(&watch{
		container: "c9",
		name:      "sandbox-x",
		agent:     "claude",
		meta:      audit.SessionMeta{RunID: "sandbox-x", Agent: "claude", Detached: true},
	})

	sv.tick(context.Background())

	if len(rec.written) != 1 {
		t.Fatalf("wrote %d audit lines, want the ending", len(rec.written))
	}
	got := rec.written[0]
	if !got.Finished {
		t.Error("the ending is not marked finished, so a reader still cannot tell a result from a placeholder")
	}
	if got.ExitCode != 7 {
		t.Errorf("exit code = %d, want the container's 7", got.ExitCode)
	}
	if got.RunID != "sandbox-x" {
		t.Errorf("RunID = %q, want the launch line's so the two can be matched", got.RunID)
	}
	if got.Duration != 2*time.Minute {
		t.Errorf("duration = %s, want the container's own 2m — not this process's uptime", got.Duration)
	}
}

// A run whose ending nobody saw stays unrecorded rather than being guessed.
func TestARunWithNoLaunchRecordWritesNoEnding(t *testing.T) {
	s, fr := newTestServer(t)
	rec := &recordingSink{}
	s.Session.Audit = rec
	sv := s.sv()

	fr.containers = append(fr.containers, runtime.ContainerInfo{
		ID: "c1", Labels: map[string]string{sandbox.LabelCLI: "1"},
		State: "exited", ExitCode: 1, FinishedAt: time.Now(),
	})
	// No meta: this is the shape of a watch registered by something that never
	// launched the run — there is nothing to complete.
	sv.supervise(&watch{container: "c1", name: "sandbox-x", agent: "claude"})

	sv.tick(context.Background())

	if len(rec.written) != 0 {
		t.Errorf("wrote %d audit lines for a run it has no record of", len(rec.written))
	}
}

type recordingSink struct{ written []audit.SessionMeta }

func (r *recordingSink) RecordSession(m audit.SessionMeta) { r.written = append(r.written, m) }

// A retried attempt's ending is recorded too.
//
// The first version started it with Session.Start and kept no record, so its
// launch line stayed unfinished forever — which left exactly the failover
// episodes the Routing panels report sitting in the "not recorded" bucket, the
// one case the whole feature exists to describe.
func TestARetryCarriesARecordItsEndingCanComplete(t *testing.T) {
	_, fr, sv, _ := supervised(t, 1, "tree-before")

	sv.tick(context.Background())

	if len(fr.started) != 1 {
		t.Fatalf("started %d containers, want the fallback", len(fr.started))
	}
	sv.mu.Lock()
	defer sv.mu.Unlock()
	for _, w := range sv.watched {
		if w.meta.RunID == "" {
			t.Error("the retry has no launch record, so its ending would never be written")
		}
		if w.meta.Finished {
			t.Error("the retry's launch record claims to be finished")
		}
	}
}

// A duration nobody measured is not published as one.
//
// The record is copied from the launch line, whose duration is how long
// `docker run` took — fine while the line says "not finished", and a lie the
// moment it says otherwise.
func TestAnUnmeasuredRunReportsNoDuration(t *testing.T) {
	s, fr := newTestServer(t)
	rec := &recordingSink{}
	s.Session.Audit = rec
	sv := s.sv()

	fr.containers = append(fr.containers, runtime.ContainerInfo{
		ID: "c4", Labels: map[string]string{sandbox.LabelCLI: "1"},
		State: "exited", ExitCode: 0, FinishedAt: time.Now(),
		// No StartedAt: the engine did not say when it began.
	})
	sv.supervise(&watch{
		container: "c4",
		meta: audit.SessionMeta{
			RunID: "run-x", Detached: true,
			Duration: 250 * time.Millisecond, // how long the launch call took
		},
	})

	sv.tick(context.Background())

	if len(rec.written) != 1 {
		t.Fatalf("wrote %d lines, want the ending", len(rec.written))
	}
	if got := rec.written[0].Duration; got != 0 {
		t.Errorf("duration = %s, want 0 — the launch latency is not the run's length", got)
	}
}
