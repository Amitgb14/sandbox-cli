package workspace

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// The loop that every connected client shares: a failure surfaces only if no
// success followed it, a checkpoint cut short by stop is not a failure, and
// only a checkpoint that fetched something new is recorded.
func TestCheckpointsLoop(t *testing.T) {
	run := func(steps []func(context.Context) (string, bool, error)) ([]string, error) {
		var mu sync.Mutex
		var taken []string
		i := 0
		finished := make(chan struct{})
		stop := Checkpoints(context.Background(), time.Millisecond,
			func(ctx context.Context) (string, bool, error) {
				mu.Lock()
				defer mu.Unlock()
				if i >= len(steps) {
					if i == len(steps) {
						close(finished)
					}
					i++
					<-ctx.Done()
					return "", false, ctx.Err() // cut short by stop
				}
				i++
				return steps[i-1](ctx)
			},
			func(ref string) { taken = append(taken, ref) })
		<-finished
		err := stop()
		return taken, err
	}
	ok := func(ref string, fetched bool) func(context.Context) (string, bool, error) {
		return func(context.Context) (string, bool, error) { return ref, fetched, nil }
	}
	bad := func(context.Context) (string, bool, error) { return "", false, errors.New("guest busy") }

	taken, err := run([]func(context.Context) (string, bool, error){bad, ok("r1", true), ok("r1", false)})
	if err != nil {
		t.Errorf("a failure followed by a success was reported: %v", err)
	}
	if len(taken) != 1 || taken[0] != "r1" {
		t.Errorf("taken %v: only a checkpoint that fetched something is recorded", taken)
	}

	if _, err := run([]func(context.Context) (string, bool, error){ok("r1", true), bad}); err == nil {
		t.Error("a failure that nothing followed was not reported")
	}
}
