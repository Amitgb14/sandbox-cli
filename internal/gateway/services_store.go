package gateway

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// What the service controller keeps in the state file, so a gateway that
// restarts resumes every service where it was: the specs, the revision
// replicas are kept at and a rollout in progress, which sandbox is which
// replica, and the replicas still to be terminated. Health is not kept: a
// restarted gateway checks every replica again before routing to it.

type serviceState struct {
	Records []serviceRecord `json:"records"`
	// Retiring are replica sandboxes taken out of their service and not yet
	// confirmed terminated — their node was down, or did not answer. They
	// are terminated as soon as it answers, so a replica replaced while its
	// node was lost does not run on, unowned by any service, when it returns.
	Retiring []string `json:"retiring,omitempty"`
}

// serviceRecord is one service.
type serviceRecord struct {
	Name   string `json:"name"`
	User   string `json:"user"`
	Tenant string `json:"tenant,omitempty"`
	// Spec is the newest spec as sent. Replicas, Public and Placement are
	// read from it; what a replica's sandbox is comes from its revision.
	Spec     api.ServiceSpec `json:"spec"`
	Revision int             `json:"revision"`
	// Serving is the revision replicas are kept at; Target, while a rollout
	// is in progress, the one they are moving to.
	Serving revision  `json:"serving"`
	Target  *revision `json:"target,omitempty"`
	// Older are revisions neither serving nor targeted that still have
	// replicas — a failed or superseded rollout's — so each is checked by
	// the spec it was made from until it is replaced.
	Older    []revision          `json:"older,omitempty"`
	Rollout  *api.ServiceRollout `json:"rollout,omitempty"`
	Members  []replicaRecord     `json:"members"`
	Restarts int                 `json:"restarts,omitempty"`
	Created  time.Time           `json:"created"`
	Updated  time.Time           `json:"updated"`
}

type revision struct {
	Rev  int             `json:"rev"`
	Spec api.ServiceSpec `json:"spec"`
}

// replicaRecord is one replica: its sandbox, where, which revision, and the
// handle of the command started in it.
type replicaRecord struct {
	Sandbox  string    `json:"sandbox"`
	Node     string    `json:"node"`
	Rev      int       `json:"rev"`
	PID      int       `json:"pid,omitempty"`
	Restarts int       `json:"restarts,omitempty"`
	Created  time.Time `json:"created"`
}

// clone copies a record deeply: specs hold maps and slices, and the
// controller's copy and the store's must not share them.
func (r serviceRecord) clone() serviceRecord {
	data, err := json.Marshal(r)
	if err != nil {
		panic(err) // a record is plain data
	}
	var out serviceRecord
	if err := json.Unmarshal(data, &out); err != nil {
		panic(err)
	}
	return out
}

// pruneOlder drops older revisions no replica is left of.
func (r *serviceRecord) pruneOlder() {
	r.Older = slices.DeleteFunc(r.Older, func(o revision) bool {
		return !slices.ContainsFunc(r.Members, func(m replicaRecord) bool { return m.Rev == o.Rev })
	})
}

func (s *FileStore) svcState() *serviceState {
	if s.st.Services == nil {
		s.st.Services = &serviceState{}
	}
	return s.st.Services
}

// Services returns every service record.
func (s *FileStore) Services() []serviceRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.st.Services == nil {
		return nil
	}
	out := make([]serviceRecord, len(s.st.Services.Records))
	for i, r := range s.st.Services.Records {
		out[i] = r.clone()
	}
	return out
}

// PutService adds a service record, or replaces the one with its tenant and
// name.
func (s *FileStore) PutService(r serviceRecord) error { return s.PutServiceRetiring(r, nil) }

// DeleteService removes a service record and, in the same write, queues its
// replicas for termination, so no restart can find the service gone and its
// replicas forgotten.
func (s *FileStore) DeleteService(tenant, name string, retire []string) error {
	return s.change(func() error {
		st := s.svcState()
		st.Records = slices.DeleteFunc(st.Records, func(r serviceRecord) bool { return r.Tenant == tenant && r.Name == name })
		st.Retiring = appendNew(st.Retiring, retire...)
		return nil
	})
}

// PutServiceRetiring is PutService with replicas queued for termination in
// the same write: a replica leaves its service and joins the queue at once.
func (s *FileStore) PutServiceRetiring(r serviceRecord, retire []string) error {
	r = r.clone()
	return s.change(func() error {
		st := s.svcState()
		found := false
		for i := range st.Records {
			if st.Records[i].Tenant == r.Tenant && st.Records[i].Name == r.Name {
				st.Records[i], found = r, true
			}
		}
		if !found {
			st.Records = append(st.Records, r)
		}
		st.Retiring = appendNew(st.Retiring, retire...)
		return nil
	})
}

// Retire queues sandboxes for termination.
func (s *FileStore) Retire(ids ...string) error {
	return s.change(func() error {
		st := s.svcState()
		st.Retiring = appendNew(st.Retiring, ids...)
		return nil
	})
}

// Retiring lists the replica sandboxes waiting to be terminated.
func (s *FileStore) Retiring() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.st.Services == nil {
		return nil
	}
	return slices.Clone(s.st.Services.Retiring)
}

// Retired drops sandboxes from the queue once terminated (or gone).
func (s *FileStore) Retired(ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.change(func() error {
		st := s.svcState()
		st.Retiring = slices.DeleteFunc(st.Retiring, func(id string) bool { return slices.Contains(ids, id) })
		return nil
	})
}

func appendNew(list []string, ids ...string) []string {
	for _, id := range ids {
		if !slices.Contains(list, id) {
			list = append(list, id)
		}
	}
	return list
}
