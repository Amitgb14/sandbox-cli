package server

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// A pool keeps sandboxes booted ahead of the requests that will want them, so
// a create is a claim rather than a boot. What makes that sound is that
// nothing about a sandbox a client can choose is baked into the VM at boot
// except its shape — image, resources and network policy. Its environment is
// applied by this server to every process it starts; its name, labels and idle
// timeout are this server's records. So a pooled sandbox is handed only to a
// request that resolves to exactly the shape it was booted with, and anything
// else — volumes, a bind mount, a snapshot, another network policy — boots
// fresh as before. A pooled VM has run nothing but the guest agent.
//
// Pooled sandboxes are not in s.sandboxes until claimed: they are not listed,
// not idle-reaped and not addressable. A restart of sandboxd forgets them like
// every other VM, and the backend removes them.

type pool struct {
	mu       sync.Mutex
	template backend.Spec // ID and Env unset
	key      string
	ready    []string
	filling  int
	size     int
	failures int
}

// poolKey is the part of a resolved spec that is fixed at boot. Env and the
// idle timeout are deliberately absent: they are applied by the server.
func poolKey(s backend.Spec) string {
	if s.FromSnapshot != "" || len(s.Volumes) > 0 {
		return ""
	}
	b, _ := json.Marshal(struct {
		Image    string
		CPUs     float64
		MemoryMB int
		DiskMB   int
		Network  api.NetworkPolicy
	}{s.Image, s.CPUs, s.MemoryMB, s.DiskMB, s.Network})
	return string(b)
}

// startPools resolves each configured pool's shape and keeps it filled.
func (s *Server) startPools() {
	for _, cfg := range s.Policy.Pools {
		bs, err := spec.Resolve(api.CreateSandboxRequest{Image: cfg.Image}, s.Policy, "")
		if err != nil {
			s.logf("pool %s: %v; not pooling it", cfg.Image, err)
			continue
		}
		bs.Env = nil
		p := &pool{template: bs, key: poolKey(bs), size: cfg.Size}
		s.pools = append(s.pools, p)
		go s.fill(p)
	}
}

// claimPooled hands out a ready sandbox of this shape, or "".
func (s *Server) claimPooled(bs backend.Spec) string {
	key := poolKey(bs)
	if key == "" {
		return ""
	}
	for _, p := range s.pools {
		if p.key != key {
			continue
		}
		p.mu.Lock()
		if len(p.ready) == 0 {
			p.mu.Unlock()
			return ""
		}
		id := p.ready[0]
		p.ready = p.ready[1:]
		p.mu.Unlock()
		go s.fill(p)
		return id
	}
	return ""
}

// fill boots sandboxes until the pool is full. A backend that keeps failing
// is retried with a growing delay rather than in a loop.
func (s *Server) fill(p *pool) {
	for {
		p.mu.Lock()
		if len(p.ready)+p.filling >= p.size {
			p.mu.Unlock()
			return
		}
		p.filling++
		delay := time.Duration(0)
		if p.failures > 0 {
			delay = time.Duration(min(p.failures, 6)) * 5 * time.Second
		}
		p.mu.Unlock()
		time.Sleep(delay)

		bs := p.template
		bs.ID = spec.NewID()
		err := s.Backend.Create(context.Background(), bs)
		p.mu.Lock()
		p.filling--
		if err != nil {
			p.failures++
			p.mu.Unlock()
			s.logf("pool %s: booting a sandbox: %v", bs.Image, err)
			continue
		}
		p.failures = 0
		p.ready = append(p.ready, bs.ID)
		p.mu.Unlock()
	}
}

func (s *Server) logf(format string, a ...any) {
	if s.Logf != nil {
		s.Logf(format, a...)
	}
}
