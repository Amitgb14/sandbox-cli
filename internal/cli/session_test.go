package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// fakeSessionRuntime stands in for the container backend, so every rule below is
// assertable without a daemon.
type fakeSessionRuntime struct {
	containers []runtime.ContainerInfo
	listErr    error
	askedFor   map[string]string

	stopped  []string
	killed   []string
	removed  []string
	attached []string
}

func (f *fakeSessionRuntime) Containers(_ context.Context, labels map[string]string) ([]runtime.ContainerInfo, error) {
	f.askedFor = labels
	return f.containers, f.listErr
}
func (f *fakeSessionRuntime) Logs(_ context.Context, id string, _ bool, _, _ io.Writer) error {
	return nil
}
func (f *fakeSessionRuntime) Stop(_ context.Context, id string) error {
	f.stopped = append(f.stopped, id)
	return nil
}
func (f *fakeSessionRuntime) Kill(_ context.Context, id string) error {
	f.killed = append(f.killed, id)
	return nil
}
func (f *fakeSessionRuntime) Remove(_ context.Context, id string) error {
	f.removed = append(f.removed, id)
	return nil
}
func (f *fakeSessionRuntime) Attach(_ context.Context, id string, _ io.Reader, _, _ io.Writer) error {
	f.attached = append(f.attached, id)
	return nil
}

// sess builds a container the way docker would report one.
func sess(id, name, branch, state string) runtime.ContainerInfo {
	c := runtime.ContainerInfo{
		ID:     id,
		Name:   name,
		State:  state,
		Labels: map[string]string{sandbox.LabelCLI: "1"},
	}
	if branch != "" {
		c.Labels[sandbox.LabelBranch] = branch
	}
	return c
}

// TestResolveSessionAcceptsTheThreeThingsAUserHasInFrontOfThem: the id from the
// listing, the container name, and the branch — because the branch is how a
// detached or fleet run is addressed everywhere else in the tool.
func TestResolveSessionAcceptsTheThreeThingsAUserHasInFrontOfThem(t *testing.T) {
	infos := []runtime.ContainerInfo{
		sess("a1b2c3d4e5f6a7b8", "sandbox-repo-feature-a", "feature-a", "running"),
		sess("ffff0000ffff0000", "sandbox-repo-other", "other", "running"),
	}
	for _, ref := range []string{
		"a1b2c3d4e5f6a7b8",       // full id
		"a1b2c3d4e5f6",           // the short id the listing prints
		"a1b2",                   // a unique prefix
		"sandbox-repo-feature-a", // the container name
		"feature-a",              // the branch
	} {
		got, err := resolveSession(infos, ref)
		if err != nil {
			t.Errorf("resolveSession(%q): %v", ref, err)
			continue
		}
		if got.ID != "a1b2c3d4e5f6a7b8" {
			t.Errorf("resolveSession(%q) = %s, want the feature-a session", ref, got.ID)
		}
	}
}

// TestResolveSessionNeverReachesAContainerWeDidNotStart is the security-relevant
// one. The reference is matched against a listing filtered by our own label and
// is never handed to the engine to resolve — so a name that means something to
// docker, but nothing to us, resolves to nothing rather than to somebody's
// database.
func TestResolveSessionNeverReachesAContainerWeDidNotStart(t *testing.T) {
	ours := []runtime.ContainerInfo{sess("aaaa1111", "sandbox-repo-main", "main", "running")}

	if _, err := resolveSession(ours, "postgres"); err == nil {
		t.Fatal("a container sandbox-cli did not start must not resolve")
	}
	// And the listing that feeds it asks for the label, not for a name prefix.
	f := &fakeSessionRuntime{containers: ours}
	if _, err := sandboxSessions(context.Background(), f, "docker", true); err != nil {
		t.Fatalf("sandboxSessions: %v", err)
	}
	if f.askedFor[sandbox.LabelCLI] != "1" {
		t.Errorf("sessions were listed with %v, want a filter on %s", f.askedFor, sandbox.LabelCLI)
	}
}

// TestResolveSessionRefusesAmbiguity: stopping the wrong agent is not undone by
// running the command again, so a guess is not worth the keystrokes it saves.
// The error has to name the candidates, or the refusal is a dead end.
func TestResolveSessionRefusesAmbiguity(t *testing.T) {
	infos := []runtime.ContainerInfo{
		sess("aaaa1111", "sandbox-one-main", "main", "exited"),
		sess("bbbb2222", "sandbox-two-main", "main", "exited"),
	}
	_, err := resolveSession(infos, "main")
	if err == nil {
		t.Fatal("two sessions on the same branch must not resolve to one of them")
	}
	for _, want := range []string{"aaaa1111", "bbbb2222"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must list %s so the user can pick: %v", want, err)
		}
	}
}

// TestResolveSessionPrefersTheRunningOne is the single exception to the refusal
// above: a branch names work in progress, and it cannot reasonably mean the
// container that finished yesterday when one is still going.
func TestResolveSessionPrefersTheRunningOne(t *testing.T) {
	infos := []runtime.ContainerInfo{
		sess("old00000", "sandbox-repo-feature", "feature", "exited"),
		sess("new11111", "sandbox-repo-feature", "feature", "running"),
	}
	got, err := resolveSession(infos, "feature")
	if err != nil {
		t.Fatalf("resolveSession: %v", err)
	}
	if got.ID != "new11111" {
		t.Errorf("resolved %s, want the running session", got.ID)
	}

	// Two running ones is genuine ambiguity again.
	infos[0].State = "running"
	if _, err := resolveSession(infos, "feature"); err == nil {
		t.Error("two running sessions on one branch must refuse")
	}
}

// TestExactMatchOutranksPrefix: a full id must never be out-voted by something
// it happens to be a prefix of.
func TestExactMatchOutranksPrefix(t *testing.T) {
	infos := []runtime.ContainerInfo{
		sess("abc", "sandbox-short", "", "running"),
		sess("abcdef", "sandbox-long", "", "running"),
	}
	got, err := resolveSession(infos, "abc")
	if err != nil {
		t.Fatalf("resolveSession: %v", err)
	}
	if got.ID != "abc" {
		t.Errorf("resolved %s, want the exact match", got.ID)
	}
}

// TestOnlyRunningSessionIsInferredForReadingButNotForStopping pins the
// asymmetry: reading the wrong session costs a second, stopping the wrong agent
// costs its work, so only the reading commands may infer their target.
func TestOnlyRunningSessionIsInferredForReadingButNotForStopping(t *testing.T) {
	infos := []runtime.ContainerInfo{
		sess("aaaa1111", "sandbox-repo-main", "main", "running"),
		sess("bbbb2222", "sandbox-repo-old", "old", "exited"),
	}
	got, err := pickSession(infos, nil)
	if err != nil {
		t.Fatalf("one running session should be inferrable: %v", err)
	}
	if got.ID != "aaaa1111" {
		t.Errorf("inferred %s, want the running session", got.ID)
	}

	// kill builds its targets from explicit references only; with none, the
	// command refuses before it gets here (Args) — what matters is that the
	// helper never invents one.
	targets, err := killTargets(infos, nil, false)
	if err != nil {
		t.Fatalf("killTargets: %v", err)
	}
	if len(targets) != 0 {
		t.Errorf("kill with no reference resolved %d targets, want none", len(targets))
	}
}

// TestKillTargetsAreAllOrNothing: a partial kill from one typo leaves the user
// unsure which half ran, and re-running the corrected command stops things
// twice.
func TestKillTargetsAreAllOrNothing(t *testing.T) {
	infos := []runtime.ContainerInfo{
		sess("aaaa1111", "sandbox-repo-a", "a", "running"),
		sess("bbbb2222", "sandbox-repo-b", "b", "running"),
	}
	if _, err := killTargets(infos, []string{"a", "nope"}, false); err == nil {
		t.Fatal("one unresolvable reference must fail the whole command")
	}
	// Naming one session two ways stops it once.
	got, err := killTargets(infos, []string{"a", "aaaa1111", "sandbox-repo-a"}, false)
	if err != nil {
		t.Fatalf("killTargets: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("resolved %d targets, want 1 after collapsing duplicates", len(got))
	}
}

// TestKillIsGracefulUnlessForced: an agent gets the grace period to finish the
// file it was writing, and --force is how you say otherwise.
func TestKillIsGracefulUnlessForced(t *testing.T) {
	f := &fakeSessionRuntime{}
	targets := []runtime.ContainerInfo{sess("aaaa1111", "sandbox-repo-a", "a", "running")}

	if err := stopSessions(context.Background(), f, targets, false, io.Discard); err != nil {
		t.Fatalf("stopSessions: %v", err)
	}
	if len(f.stopped) != 1 || len(f.killed) != 0 {
		t.Errorf("default kill used stop=%v kill=%v, want a graceful stop", f.stopped, f.killed)
	}

	f = &fakeSessionRuntime{}
	if err := stopSessions(context.Background(), f, targets, true, io.Discard); err != nil {
		t.Fatalf("stopSessions --force: %v", err)
	}
	if len(f.killed) != 1 || len(f.stopped) != 0 {
		t.Errorf("--force used stop=%v kill=%v, want SIGKILL", f.stopped, f.killed)
	}
}

// TestKillSkipsSessionsThatAlreadyFinished — asking for a state something is
// already in is not an error, and must not report a stop that never happened.
func TestKillSkipsSessionsThatAlreadyFinished(t *testing.T) {
	f := &fakeSessionRuntime{}
	var out bytes.Buffer
	targets := []runtime.ContainerInfo{sess("aaaa1111", "sandbox-repo-a", "a", "exited")}
	if err := stopSessions(context.Background(), f, targets, false, &out); err != nil {
		t.Fatalf("stopSessions: %v", err)
	}
	if len(f.stopped) != 0 || len(f.killed) != 0 {
		t.Errorf("an exited session was signalled: stop=%v kill=%v", f.stopped, f.killed)
	}
	if !strings.Contains(out.String(), "already") {
		t.Errorf("output should say nothing was stopped, got %q", out.String())
	}
}

// TestListingHidesFinishedSessionsUnlessAsked, and treats an odd state as live:
// a listing that hides a container is worse than one showing a state you have to
// read, because a container nobody can list is one nobody can stop.
func TestListingHidesFinishedSessionsUnlessAsked(t *testing.T) {
	f := &fakeSessionRuntime{containers: []runtime.ContainerInfo{
		sess("aaaa1111", "sandbox-a", "a", "running"),
		sess("bbbb2222", "sandbox-b", "b", "exited"),
		sess("cccc3333", "sandbox-c", "c", "paused"),
		sess("dddd4444", "sandbox-d", "d", "created"),
	}}
	live, err := sandboxSessions(context.Background(), f, "docker", false)
	if err != nil {
		t.Fatalf("sandboxSessions: %v", err)
	}
	if len(live) != 3 {
		t.Errorf("listed %d sessions, want the running, paused and created ones: %+v", len(live), live)
	}
	all, err := sandboxSessions(context.Background(), f, "docker", true)
	if err != nil {
		t.Fatalf("sandboxSessions --all: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("--all listed %d sessions, want 4", len(all))
	}
}

// TestListingErrorNamesTheEngine: an empty table reads as "nothing running",
// which is the wrong answer when the daemon is simply down.
func TestListingErrorNamesTheEngine(t *testing.T) {
	f := &fakeSessionRuntime{listErr: errors.New("cannot connect")}
	_, err := sandboxSessions(context.Background(), f, "podman", false)
	if err == nil || !strings.Contains(err.Error(), "podman daemon") {
		t.Errorf("error should name the engine that could not be reached, got %v", err)
	}
}

// TestSessionListingRendersOneRowPerSessionWhateverALabelSays carries forward
// the regression the old text-parsing listing was built around: a label is text
// from the repository, and it must not be able to forge a row, forge a column,
// or instruct the terminal reading it.
func TestSessionListingRendersOneRowPerSessionWhateverALabelSays(t *testing.T) {
	hostile := sess("aaaa1111", "sandbox-a", "main\nsandbox-fake\tUp\tforged", "running")
	hostile.Labels[sandbox.LabelAgent] = "claude\x1b]0;pwned\x07"
	rows := []runtime.ContainerInfo{hostile, sess("bbbb2222", "sandbox-b", "", "exited")}

	var out bytes.Buffer
	if err := renderSessions(&out, rows, true, time.Now()); err != nil {
		t.Fatalf("renderSessions: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 3 { // header + one row per session
		t.Errorf("rendered %d lines, want a header and two rows:\n%s", len(lines), out.String())
	}
	if strings.ContainsAny(out.String(), "\x1b\x07") {
		t.Error("a label put an escape sequence on the user's terminal")
	}
	// A session with no branch or agent gets a column, not a gap.
	if !strings.Contains(lines[2], "-") {
		t.Errorf("missing labels should render as a dash: %q", lines[2])
	}
}

// TestSessionListingSaysWhatToDoWhenEmpty — "none" is a fact; the next command
// is what the reader actually needs.
func TestSessionListingSaysWhatToDoWhenEmpty(t *testing.T) {
	var running, all bytes.Buffer
	if err := renderSessions(&running, nil, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(running.String(), "--all") {
		t.Errorf("an empty running listing should point at --all: %q", running.String())
	}
	if err := renderSessions(&all, nil, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(all.String(), "sandbox-cli") {
		t.Errorf("an empty full listing should say how to start one: %q", all.String())
	}
}

// TestSessionUptimeMeasuresTheRightWindow: a running session has been up since
// it started, a finished one ran for as long as it ran — reporting "now minus
// start" for a container that exited an hour ago would make every dead session
// look like the busiest one on the machine.
func TestSessionUptimeMeasuresTheRightWindow(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

	live := sess("a", "sandbox-a", "", "running")
	live.StartedAt = now.Add(-90 * time.Second)
	if got := sessionUptime(live, now); got != "1m30s" {
		t.Errorf("running uptime = %q, want 1m30s", got)
	}

	done := sess("b", "sandbox-b", "", "exited")
	done.StartedAt = now.Add(-3 * time.Hour)
	done.FinishedAt = now.Add(-2 * time.Hour)
	if got := sessionUptime(done, now); got != "1h0m" {
		t.Errorf("finished uptime = %q, want the hour it ran, not the hour since", got)
	}

	never := sess("c", "sandbox-c", "", "created")
	if got := sessionUptime(never, now); got != "-" {
		t.Errorf("a session that never started = %q, want a dash", got)
	}
}

// TestSessionStatusCarriesTheExitCode — "exited" on its own is the one thing
// nobody ever wants to know on its own.
func TestSessionStatusCarriesTheExitCode(t *testing.T) {
	c := sess("a", "sandbox-a", "", "exited")
	c.ExitCode = 2
	if got := sessionStatus(c); got != "exited (2)" {
		t.Errorf("status = %q, want the exit code with it", got)
	}
	if got := sessionStatus(sess("b", "sandbox-b", "", "running")); got != "running" {
		t.Errorf("status = %q, want running", got)
	}
	if got := sessionStatus(sess("c", "sandbox-c", "", "")); got != "?" {
		t.Errorf("an unreadable state = %q, want ?", got)
	}
}

// TestAttachSaysWhatItCannotDo: both notes exist because the alternative is
// learning the answer the expensive way — typing into a container that is not
// listening, or pressing Ctrl-C to stop watching and finding you stopped the
// work.
func TestAttachSaysWhatItCannotDo(t *testing.T) {
	detached := sess("a", "sandbox-a", "", "running") // OpenStdin false: --detach
	notes := strings.Join(attachNotes(detached), "\n")
	if !strings.Contains(notes, "Ctrl-C detaches") {
		t.Errorf("attach must say how to leave without killing: %q", notes)
	}
	if !strings.Contains(notes, "no keyboard") {
		t.Errorf("attaching to a detached run must say it cannot type: %q", notes)
	}

	interactive := sess("b", "sandbox-b", "", "running")
	interactive.OpenStdin = true
	if n := strings.Join(attachNotes(interactive), "\n"); strings.Contains(n, "no keyboard") {
		t.Errorf("a session with stdin open can be typed at: %q", n)
	}
}

func TestHumanDurationNeverPrintsGoDurations(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{-time.Second, "0s"},
		{45 * time.Second, "45s"},
		{90 * time.Second, "1m30s"},
		{time.Hour, "1h0m"},
		{25 * time.Hour, "1d1h"},
	} {
		if got := humanDuration(tc.in); got != tc.want {
			t.Errorf("humanDuration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
