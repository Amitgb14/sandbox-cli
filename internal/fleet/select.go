package fleet

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// selectTasks narrows a fleet file to the tasks this invocation should start.
//
// Two narrowings, and they compose: --only names branches, --resume drops the
// ones already done. Both answer the same question — "I do not want to re-run
// the whole file" — from opposite ends, and a `fleet run --only a,b --resume`
// meaning "retry those two unless they already worked" is a reasonable thing to
// type.
func (r *Runner) selectTasks(ctx context.Context, spec Spec, opts LaunchOptions) ([]Task, error) {
	tasks := spec.Tasks
	if len(opts.Only) > 0 {
		var err error
		if tasks, err = only(tasks, opts.Only); err != nil {
			return nil, err
		}
	}
	if !opts.Resume {
		return tasks, nil
	}
	return r.unfinished(ctx, tasks)
}

// only keeps the named branches, in the file's order rather than the order they
// were typed — the file's order is the one max_parallel schedules in, and a
// --only that quietly reordered the fleet would be a different run.
//
// A name that matches nothing is an error naming the branches that do exist. The
// alternative is launching zero containers and printing nothing, which is
// indistinguishable from a fleet that ran perfectly.
func only(tasks []Task, branches []string) ([]Task, error) {
	want := map[string]bool{}
	for _, b := range branches {
		if b = strings.TrimSpace(b); b != "" {
			want[b] = true
		}
	}
	var out []Task
	for _, t := range tasks {
		if want[t.Branch] {
			delete(want, t.Branch)
			out = append(out, t)
		}
	}
	if len(want) > 0 {
		var missing, known []string
		for b := range want {
			missing = append(missing, b)
		}
		for _, t := range tasks {
			known = append(known, t.Branch)
		}
		return nil, fmt.Errorf("no task in this fleet file for %s; it has: %s",
			strings.Join(quoted(missing), ", "), strings.Join(known, ", "))
	}
	return out, nil
}

// unfinished drops the tasks a --resume should not repeat: the ones still
// running, and the ones whose last container exited 0.
//
// Exit 0 is the only thing treated as done, and that is the point of `verify:`
// — a task that declared one and failed it exited VerifyFailedExit, so a resume
// retries it, which is what someone re-running an interrupted fleet wants. A
// task with no verify exits with whatever the agent returned, so "it stopped
// cleanly" is all resume can know about it; that is the same limit `land`
// reports as *unverified* rather than passed.
//
// Read from the container, not from the worktree: the worktree is what the agent
// was editing.
func (r *Runner) unfinished(ctx context.Context, tasks []Task) ([]Task, error) {
	var out []Task
	for _, t := range tasks {
		c, err := r.latestFor(ctx, t.Branch)
		if err != nil {
			return nil, err
		}
		switch {
		case c == nil:
			out = append(out, t) // never started, or its container was reaped
		case c.Running():
			r.logf("%s: an agent is already running (%s); leaving it alone", t.Branch, c.Name)
		case c.State == "exited" && c.ExitCode == 0:
			r.logf("%s: already finished successfully; skipping", t.Branch)
		default:
			out = append(out, t)
		}
	}
	return out, nil
}

// HostSizer is the part of a runtime backend that can say how much memory the
// machine has. Optional: a backend that cannot answer simply skips the capacity
// check below, which is advice rather than a boundary.
type HostSizer interface {
	HostMemoryBytes(ctx context.Context) (int64, bool)
}

// checkCapacity refuses a fleet whose agents cannot all fit in the machine's
// memory at once.
//
// The arithmetic is deliberately the *concurrent* demand — how many agents run
// at the same time, times the cap each one gets — and not the task count. That
// is exactly what `max_parallel` exists to bound, so a twenty-task fleet with
// `max_parallel: 2` is a perfectly ordinary thing to run on a laptop and must
// not be refused for being long.
//
// Refusing rather than warning, because the failure this prevents is not a
// message anyone reads: the containers start, the machine swaps, and the kernel
// kills whichever agent was unlucky, hours in, leaving a fleet whose branches
// exited with codes nobody can interpret. The two ways out are named in the
// message and both are one line of the fleet file.
//
// A host that cannot be measured proceeds silently. This is resource sanity, not
// a security control — the profile rule that an unanswerable question counts as
// a failure belongs to the controls that define the boundary, and applying it
// here would refuse fleets on any machine whose engine reports `info`
// differently.
func (r *Runner) checkCapacity(ctx context.Context, spec Spec, taskCount int) error {
	sizer, ok := r.Session.Runtime.(HostSizer)
	if !ok {
		return nil
	}
	total, ok := sizer.HostMemoryBytes(ctx)
	if !ok || total <= 0 {
		return nil
	}

	// The widest per-task cap, not an average: with max_parallel below the task
	// count it is unknowable which tasks overlap, so the honest bound is the worst
	// case. A fleet where one task raises its own memory is exactly the case this
	// gets wrong if it averages.
	var worst int64
	var uncapped []string
	for _, t := range spec.Tasks {
		m := spec.LimitsFor(t).Memory
		if m == "" {
			uncapped = append(uncapped, t.Branch)
			continue
		}
		b, err := parseSize(m)
		if err != nil {
			return fmt.Errorf("%s: memory %q is not a size docker understands (e.g. 4g, 512m)", t.Branch, m)
		}
		if b > worst {
			worst = b
		}
	}
	// An uncapped task can use the whole machine by definition, so there is no
	// arithmetic to do and nothing to refuse — the fleet file asked for that with
	// `memory: "0"`, which is a deliberate act. Say so once rather than silently
	// skipping the check the user may think is protecting them.
	if len(uncapped) > 0 {
		r.logf("memory is uncapped for %s, so the fleet's total footprint is unbounded",
			strings.Join(uncapped, ", "))
		return nil
	}
	if worst == 0 {
		return nil
	}

	concurrent := int64(spec.Concurrency())
	if n := int64(taskCount); n < concurrent {
		concurrent = n
	}
	need := worst * concurrent
	if need <= total {
		return nil
	}
	return fmt.Errorf("this fleet asks for %s (%d agents at once × %s) on a machine with %s\n"+
		"  lower max_parallel, or lower the memory cap, and run it again",
		humanSize(need), concurrent, humanSize(worst), humanSize(total))
}

// parseSize reads a docker memory value ("4g", "512m", "2048") as bytes.
//
// Docker's own spelling, deliberately: this exists to reason about the very
// string that will be handed to `--memory`, so a value it accepts and this
// rejects would refuse a fleet that would have run. Suffixes are b/k/m/g,
// case-insensitive, optionally with the "b" docker also tolerates (gb == g).
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	s = strings.TrimSuffix(s, "b")
	mult := int64(1)
	if s != "" {
		switch s[len(s)-1] {
		case 'k':
			mult, s = 1<<10, s[:len(s)-1]
		case 'm':
			mult, s = 1<<20, s[:len(s)-1]
		case 'g':
			mult, s = 1<<30, s[:len(s)-1]
		}
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("not a size: %q", s)
	}
	return int64(n * float64(mult)), nil
}

// humanSize renders bytes the way the fleet file writes them, so the refusal
// message and the file being refused use one vocabulary.
func humanSize(b int64) string {
	switch {
	case b >= 1<<30:
		v := float64(b) / (1 << 30)
		if v == float64(int64(v)) {
			return fmt.Sprintf("%dg", int64(v))
		}
		return fmt.Sprintf("%.1fg", v)
	case b >= 1<<20:
		return fmt.Sprintf("%dm", b/(1<<20))
	default:
		return fmt.Sprintf("%db", b)
	}
}

func quoted(ss []string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, fmt.Sprintf("%q", s))
	}
	return out
}
