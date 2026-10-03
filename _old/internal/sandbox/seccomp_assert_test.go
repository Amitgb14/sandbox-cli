package sandbox

import (
	"context"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// A runtime that can answer about seccomp but knows no remedy. This is the shape
// of internal/cli's own prod launch fake, which embeds runtime.Runtime and
// declares SeccompUnavailable alone.
type answersOnly struct {
	runtime.Runtime
	unavailable bool
}

func (a answersOnly) SeccompUnavailable(context.Context) (bool, bool) {
	return a.unavailable, true
}

// prod must refuse on a host with no syscall filter even when the runtime cannot
// say how to fix it.
//
// The regression this pins is one line: the assertion in enforceSeccomp was
// widened to require SeccompRemedy alongside SeccompUnavailable, so a runtime
// without the cosmetic method fell through the `!ok` branch and prod stopped
// refusing — silently, on exactly the hosts the control exists for. A security
// decision may only depend on what it decides *on*; anything that merely
// improves the message has to be optional.
func TestRefusalDoesNotDependOnKnowingTheRemedy(t *testing.T) {
	s := &Session{Runtime: answersOnly{unavailable: true}}
	s.Cfg.Security.Seccomp = "required"

	err := s.enforceSeccomp(context.Background())
	if err == nil {
		t.Fatal("prod accepted a host with no syscall filter because the runtime knew no remedy")
	}
	if !strings.Contains(err.Error(), "no syscall filter") {
		t.Errorf("refusal does not say what is wrong: %v", err)
	}
	// And it still says something actionable rather than trailing off.
	if !strings.Contains(err.Error(), "docker info") {
		t.Errorf("no fallback advice when the runtime has none: %v", err)
	}
}

// The same runtime on a host that *does* filter must still pass, so the narrow
// assertion has not turned the check into an unconditional refusal.
func TestAFilteredHostStillPasses(t *testing.T) {
	s := &Session{Runtime: answersOnly{unavailable: false}}
	s.Cfg.Security.Seccomp = "required"

	if err := s.enforceSeccomp(context.Background()); err != nil {
		t.Fatalf("a host that applies a filter was refused: %v", err)
	}
}

// When the runtime does know the remedy, that is what the refusal carries —
// the fallback is for absence, not a replacement.
type answersAndAdvises struct{ answersOnly }

func (answersAndAdvises) SeccompRemedy() string { return "the engine-specific fix" }

func TestARuntimeThatKnowsTheRemedySuppliesIt(t *testing.T) {
	s := &Session{Runtime: answersAndAdvises{answersOnly{unavailable: true}}}
	s.Cfg.Security.Seccomp = "required"

	err := s.enforceSeccomp(context.Background())
	if err == nil {
		t.Fatal("no refusal")
	}
	if !strings.Contains(err.Error(), "the engine-specific fix") {
		t.Errorf("the runtime's own remedy was not used: %v", err)
	}
}
