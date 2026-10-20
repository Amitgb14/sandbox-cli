package server

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
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

// servesGPUs reports whether this endpoint gives sandboxes GPUs: a backend
// that can pass them through, and GPUs to pass. A backend that can with none
// given would otherwise advertise gpu with max_gpus 0, and a client that
// asked would be told its request is invalid rather than unsupported.
func (s *Server) servesGPUs() bool {
	return s.Backend.Capabilities()[api.CapGPU] && len(s.GPUs) > 0
}

// freeGPUsLocked chooses n of the node's free GPUs, in the order sandboxd was
// given them, or says why it cannot. Called with s.mu held, and the record
// that will hold them registered under the same hold, so two creates cannot
// be given one GPU.
func (s *Server) freeGPUsLocked(n int) ([]string, string) {
	free, notReady := s.freeGPUsOf(s.recordsLocked())
	if len(free) < n {
		why := fmt.Sprintf("this node has %d GPU(s) free of %d, and the sandbox asks for %d", len(free), len(s.GPUs), n)
		if len(notReady) > 0 {
			// Said, so a node set up wrong is not taken for a busy one.
			why += fmt.Sprintf("; %d not ready to be passed through (%s)", len(notReady), strings.Join(notReady, "; "))
		}
		return nil, why + "; try again when one is terminated"
	}
	return append([]string(nil), free[:n]...), ""
}

// freeGPUsOf is the node's GPUs that no record not yet terminated holds and
// that the backend says are ready, in the order sandboxd was given them, and
// why each of the others that no record holds is not ready. A device that is
// not ready is neither handed out nor counted free: given out, every create
// would get it and fail at the backend while a good one sat unused, and
// counted, a gateway would keep sending GPU creates here.
func (s *Server) freeGPUsOf(recs []*record) (free, notReady []string) {
	held := map[string]bool{}
	for _, rec := range recs {
		sb := rec.snapshot()
		if sb.State == api.StateTerminated {
			continue
		}
		for _, g := range sb.GPUs {
			held[g] = true
		}
	}
	checker, _ := s.Backend.(backend.GPUChecker)
	for _, g := range s.GPUs {
		if held[g] {
			continue
		}
		if checker != nil {
			if err := checker.CheckGPU(g); err != nil {
				notReady = append(notReady, err.Error())
				continue
			}
		}
		free = append(free, g)
	}
	return free, notReady
}

// recordsLocked is every record, with s.mu held.
func (s *Server) recordsLocked() []*record {
	recs := make([]*record, 0, len(s.sandboxes))
	for _, rec := range s.sandboxes {
		recs = append(recs, rec)
	}
	return recs
}
