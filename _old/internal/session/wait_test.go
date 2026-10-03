package session

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// A wait that expires reports `timeout`, and says what the pane actually was.
//
// The distinction the code is built around: a timeout is **not** a failure of the
// pane. A script that treats one as "the agent failed" will stop work that was merely
// slow, so the message names the state it found.
func TestWaitTimesOutWithoutBlamingThePane(t *testing.T) {
	dir, repoID := repo(t)
	s := open(t, dir)
	c := running("sandbox-x-feat", repoID, "feat", map[string]string{sandbox.LabelPane: "p_w"})
	eng := &fakeEngine{containers: []runtime.ContainerInfo{c}}

	// A running container with no transcript reports `unknown`, so waiting for
	// `done` can only expire.
	_, perr := s.wait(context.Background(), eng, protocol.PaneWaitParams{
		Pane: "p_w", States: []protocol.PaneState{protocol.StateDone}, TimeoutMS: 60,
	})
	if perr == nil {
		t.Fatal("the wait succeeded on a pane that never reached the state")
	}
	if perr.Code != protocol.CodeTimeout {
		t.Errorf("code = %q, want %q", perr.Code, protocol.CodeTimeout)
	}
	if !strings.Contains(perr.Message, "unknown") {
		t.Errorf("the timeout does not say what the pane was: %s", perr.Message)
	}
}

// The state arriving ends the wait, and the whole pane comes back — because a caller
// that waited for `done` wants the exit code next, and a second round trip to get it
// would race the container being reaped.
func TestWaitReturnsThePaneWhenTheStateArrives(t *testing.T) {
	dir, repoID := repo(t)
	s := open(t, dir)

	c := running("sandbox-x-feat", repoID, "feat", map[string]string{sandbox.LabelPane: "p_w"})
	eng := &fakeEngine{containers: []runtime.ContainerInfo{c}}

	// The container exits while the wait is in flight.
	var once sync.Once
	go func() {
		time.Sleep(100 * time.Millisecond)
		once.Do(func() {
			done := c
			done.State = "exited"
			done.ExitCode = 3
			eng.mu.Lock()
			eng.containers = []runtime.ContainerInfo{done}
			eng.mu.Unlock()
		})
	}()

	res, perr := s.wait(context.Background(), eng, protocol.PaneWaitParams{
		Pane:   "p_w",
		States: []protocol.PaneState{protocol.StateDone, protocol.StateFailed},
		// Longer than waitPoll, so the loop gets a second look.
		TimeoutMS: 10000,
	})
	if perr != nil {
		t.Fatalf("wait: %v", perr)
	}
	out, ok := res.(protocol.PaneWaitResult)
	if !ok {
		t.Fatalf("result is %T", res)
	}
	if out.State != protocol.StateFailed {
		t.Errorf("state = %q, want failed", out.State)
	}
	if out.Pane.ExitCode == nil || *out.Pane.ExitCode != 3 {
		t.Errorf("the exit code did not come back with the pane: %+v", out.Pane.ExitCode)
	}
}

// A wait for nothing is a sleep, and a wait on nothing is a typo. Both refuse rather
// than blocking until the deadline.
func TestWaitRefusesAnEmptyRequest(t *testing.T) {
	dir, _ := repo(t)
	s := open(t, dir)
	for _, p := range []protocol.PaneWaitParams{
		{States: []protocol.PaneState{protocol.StateDone}},
		{Pane: "p_x"},
	} {
		if _, perr := s.wait(context.Background(), &fakeEngine{}, p); perr == nil {
			t.Errorf("%+v was accepted", p)
		} else if perr.Code != protocol.CodeInvalid {
			t.Errorf("%+v: code = %q, want invalid", p, perr.Code)
		}
	}
}

// A pane whose container is gone will not reach anything else, so the wait ends there
// rather than at the deadline — the difference between "it finished and you asked for
// the wrong state" and five minutes of silence.
func TestWaitEndsOnAStoppedPane(t *testing.T) {
	dir, repoID := repo(t)
	s := open(t, dir)

	c := running("sandbox-x-feat", repoID, "feat", map[string]string{sandbox.LabelPane: "p_w"})
	if err := s.Adopt(context.Background(), &fakeEngine{containers: []runtime.ContainerInfo{c}}); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, perr := s.wait(context.Background(), &fakeEngine{}, protocol.PaneWaitParams{
		Pane: "p_w", States: []protocol.PaneState{protocol.StateDone}, TimeoutMS: 60000,
	})
	if perr == nil {
		t.Fatal("the wait succeeded on a stopped pane")
	}
	if perr.Code != protocol.CodeNotFound {
		t.Errorf("code = %q, want %q", perr.Code, protocol.CodeNotFound)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("waited %s on a stopped pane; it cannot change", elapsed)
	}
}

// The catalog's state comes from the conversation when there is one to read, and from
// the container alone when there is not — the same rule with less evidence, which is
// what lets a caller that sets no Transcripts keep exactly what it had.
func TestAdoptUsesTheTranscriptWhenOneIsAvailable(t *testing.T) {
	dir, repoID := repo(t)
	c := running("sandbox-x-feat", repoID, "feat", map[string]string{
		sandbox.LabelPane: "p_t", sandbox.LabelAgent: "claude",
	})
	c.OpenStdin = true // a console pane: somebody can answer it

	// No transcript source: the container alone, which cannot tell working from
	// blocked.
	bare := open(t, dir)
	if err := bare.Adopt(context.Background(), &fakeEngine{containers: []runtime.ContainerInfo{c}}); err != nil {
		t.Fatal(err)
	}
	if got := bare.Snapshot().Panes[0].State; got != protocol.StateUnknown {
		t.Errorf("with no transcript: %q, want unknown", got)
	}

	// With one, where the agent spoke and has been quiet: on a pane with a console,
	// that is somebody being waited for.
	withIt := open(t, dir)
	withIt.Transcripts = func(protocol.Pane, string) []agentctx.Message {
		return []agentctx.Message{{
			Role: "assistant", Text: "which file did you mean?",
			At: time.Now().Add(-5 * time.Minute),
		}}
	}
	if err := withIt.Adopt(context.Background(), &fakeEngine{containers: []runtime.ContainerInfo{c}}); err != nil {
		t.Fatal(err)
	}
	if got := withIt.Snapshot().Panes[0].State; got != protocol.StateBlocked {
		t.Errorf("with a transcript: %q, want blocked", got)
	}

	// And a finished container's state is its exit code, whatever the conversation
	// looked like: no transcript outranks an exit.
	done := c
	done.State = "exited"
	done.ExitCode = 0
	if err := withIt.Adopt(context.Background(), &fakeEngine{containers: []runtime.ContainerInfo{done}}); err != nil {
		t.Fatal(err)
	}
	if got := withIt.Snapshot().Panes[0].State; got != protocol.StateDone {
		t.Errorf("a finished container with a quiet transcript: %q, want done", got)
	}
}

// A poll loop must not rewrite the catalog every two seconds. One half-hour wait
// would otherwise be nine hundred identical writes and nine hundred identical lines
// in the event log.
func TestSaveIfChangedWritesOnlyOnAChange(t *testing.T) {
	dir, repoID := repo(t)
	s := open(t, dir)
	c := running("sandbox-x-feat", repoID, "feat", map[string]string{sandbox.LabelPane: "p_s"})
	eng := &fakeEngine{containers: []runtime.ContainerInfo{c}}

	if err := s.Adopt(context.Background(), eng); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIfChanged("first"); err != nil {
		t.Fatal(err)
	}
	lines := eventLines(t, s)
	if lines != 1 {
		t.Fatalf("events after the first save = %d, want 1", lines)
	}

	// Ten refreshes of unchanged state.
	for i := 0; i < 10; i++ {
		if err := s.Adopt(context.Background(), eng); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveIfChanged("poll"); err != nil {
			t.Fatal(err)
		}
	}
	if got := eventLines(t, s); got != 1 {
		t.Errorf("events after ten unchanged refreshes = %d, want 1 — the timestamps must not count as a change", got)
	}

	// And a real change still writes.
	exited := c
	exited.State = "exited"
	exited.ExitCode = 1
	if err := s.Adopt(context.Background(), &fakeEngine{containers: []runtime.ContainerInfo{exited}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIfChanged("changed"); err != nil {
		t.Fatal(err)
	}
	if got := eventLines(t, s); got != 2 {
		t.Errorf("events after a state change = %d, want 2", got)
	}
}

func eventLines(t *testing.T, s *Server) int {
	t.Helper()
	b, err := readFileOrEmpty(EventsPath(s.Dir()))
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

func readFileOrEmpty(path string) ([]byte, error) {
	b, err := osReadFile(path)
	if errors.Is(err, errNoFile) {
		return nil, nil
	}
	return b, err
}
