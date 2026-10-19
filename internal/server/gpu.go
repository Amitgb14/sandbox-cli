package server

import (
	"errors"
	"fmt"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// GPUs are given whole, one sandbox each (docs/gpu.md). Which are free is
// read from the records, as the node's other resources are (nodeStatus): a
// GPU is held by every sandbox not yet terminated that names it, so a
// terminate, a reap or a failed create gives it back by what it does to the
// record, and a restart that keeps sandboxes keeps their GPUs held.

const errGPUSuspend = "a sandbox with a GPU cannot be suspended: its VMM cannot save a passed-through device's state"

// errSnapshotGPU refuses a snapshot of a sandbox with a GPU, for the reason
// it cannot be suspended.
var errSnapshotGPU = errors.New("a sandbox with a GPU cannot be snapshotted: its VMM cannot save a passed-through device's state")

// freeGPUsLocked chooses n of the node's free GPUs, in the order sandboxd was
// given them, or says why it cannot. Called with s.mu held, and the record
// that will hold them registered under the same hold, so two creates cannot
// be given one GPU.
func (s *Server) freeGPUsLocked(n int) ([]string, string) {
	held := map[string]bool{}
	for _, rec := range s.sandboxes {
		sb := rec.snapshot()
		if sb.State == api.StateTerminated {
			continue
		}
		for _, g := range sb.GPUs {
			held[g] = true
		}
	}
	var free []string
	for _, g := range s.GPUs {
		if !held[g] {
			free = append(free, g)
		}
	}
	if len(free) < n {
		return nil, fmt.Sprintf("this node has %d GPU(s) free of %d, and the sandbox asks for %d; try again when one is terminated", len(free), len(s.GPUs), n)
	}
	return append([]string(nil), free[:n]...), ""
}
