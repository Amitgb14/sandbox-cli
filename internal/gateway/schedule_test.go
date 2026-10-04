package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

func cand(name string, freeMem int, mod ...func(*Candidate)) Candidate {
	c := Candidate{Name: name, Status: api.NodeStatus{
		Capabilities: api.Capabilities{
			Capabilities: map[string]bool{api.CapEgressAllowlist: true, api.CapVolumes: true},
			Limits:       api.Limits{MaxCPUs: 8, MaxMemoryMB: 16384, MaxDiskMB: 65536},
			Network:      api.NetworkCeiling{Ceiling: api.NetworkAllowlist},
		},
		Capacity: api.NodeResources{CPUs: 32, MemoryMB: 65536, DiskMB: 1 << 20},
		Free:     api.NodeResources{CPUs: 32, MemoryMB: freeMem, DiskMB: 1 << 20},
	}}
	for _, m := range mod {
		m(&c)
	}
	return c
}

var small = Want{CPUs: 1, MemoryMB: 1024, DiskMB: 1024}

func want(mod func(*Want)) Want {
	w := small
	mod(&w)
	return w
}

func TestSchedule(t *testing.T) {
	pooled := func(c *Candidate) { c.Status.Pooled = map[string]int{"img": 2} }
	cached := func(c *Candidate) { c.Status.Images = []string{"img"} }
	cordoned := func(c *Candidate) { c.Status.Cordoned = true }
	noVolumes := func(c *Candidate) { c.Status.Capabilities.Capabilities[api.CapVolumes] = false }
	full := func(c *Candidate) { c.Status.Free.CPUs = 0.5 }
	reserved := func(c *Candidate) { c.Reserved.MemoryMB = 40000 }
	noDiskReport := func(c *Candidate) { c.Status.Capacity.DiskMB = 0; c.Status.Free.DiskMB = 0 }
	openCeiling := func(c *Candidate) { c.Status.Capabilities.Network.Ceiling = api.NetworkOpen }

	for _, c := range []struct {
		name    string
		cands   []Candidate
		w       Want
		want    string
		wantErr error
	}{
		{"most free memory", []Candidate{cand("a", 1000), cand("b", 50000), cand("c", 20000)}, small, "b", nil},
		{"ties by name", []Candidate{cand("b", 5000), cand("a", 5000)}, small, "a", nil},
		{"a warm pool beats free memory", []Candidate{cand("a", 60000, cached), cand("b", 2000, pooled)}, want(func(w *Want) { w.Image = "img" }), "b", nil},
		{"a cached image beats free memory", []Candidate{cand("a", 60000), cand("b", 2000, cached)}, want(func(w *Want) { w.Image = "img" }), "b", nil},
		{"no image named, no preference", []Candidate{cand("a", 60000), cand("b", 2000, pooled)}, small, "a", nil},
		{"cordoned is skipped", []Candidate{cand("a", 60000, cordoned), cand("b", 2000)}, small, "b", nil},
		{"all cordoned", []Candidate{cand("a", 60000, cordoned)}, small, "", errNoCandidate},
		{"none at all", nil, small, "", errNoCandidate},
		{"capability required", []Candidate{cand("a", 60000, noVolumes), cand("b", 2000)}, want(func(w *Want) { w.Caps = []string{api.CapVolumes} }), "b", nil},
		{"no node capable", []Candidate{cand("a", 60000, noVolumes)}, want(func(w *Want) { w.Caps = []string{api.CapVolumes} }), "", errNoCapable},
		{"over a node's limits", []Candidate{cand("a", 60000)}, want(func(w *Want) { w.CPUs = 16 }), "", errNoCapable},
		{"network above the ceiling", []Candidate{cand("a", 60000), cand("b", 2000, openCeiling)}, want(func(w *Want) { w.NetworkMode = api.NetworkOpen }), "b", nil},
		{"no room for cpus", []Candidate{cand("a", 60000, full)}, small, "", errNoRoom},
		{"no room for memory", []Candidate{cand("a", 512)}, small, "", errNoRoom},
		{"reservations count", []Candidate{cand("a", 50000, reserved), cand("b", 20000)}, small, "b", nil},
		{"pinned to the holding node", []Candidate{cand("a", 60000), cand("b", 2000)}, want(func(w *Want) { w.Node = "b" }), "b", nil},
		{"pinned node not up", []Candidate{cand("a", 60000)}, want(func(w *Want) { w.Node = "b" }), "", errNoCandidate},
		{"excluded after a refusal", []Candidate{cand("a", 60000), cand("b", 2000)}, want(func(w *Want) { w.Exclude = []string{"a"} }), "b", nil},
		{"spread avoids a node holding a replica", []Candidate{cand("a", 60000), cand("b", 2000)}, want(func(w *Want) { w.Spread = map[string]int{"a": 1} }), "b", nil},
		{"spread picks the fewest", []Candidate{cand("a", 60000), cand("b", 50000), cand("c", 2000)}, want(func(w *Want) { w.Spread = map[string]int{"a": 2, "b": 1, "c": 1} }), "b", nil},
		{"spread doubles up when it must", []Candidate{cand("a", 60000), cand("b", 2000)}, want(func(w *Want) { w.Spread = map[string]int{"a": 1, "b": 1} }), "a", nil},
		{"spread does not override room", []Candidate{cand("a", 60000), cand("b", 512)}, want(func(w *Want) { w.Spread = map[string]int{"a": 1} }), "a", nil},
		{"a node reporting no disk is not refused for disk", []Candidate{cand("a", 60000, noDiskReport)}, small, "a", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := Schedule(c.cands, c.w)
			if !errors.Is(err, c.wantErr) || got != c.want {
				t.Fatalf("Schedule = %q, %v; want %q, %v", got, err, c.want, c.wantErr)
			}
		})
	}
}

// The same inputs give the same node, whatever order the nodes come in.
func TestScheduleIsDeterministic(t *testing.T) {
	a, b, c := cand("a", 5000), cand("b", 5000), cand("c", 5000)
	for _, order := range [][]Candidate{{a, b, c}, {c, b, a}, {b, c, a}} {
		if got, _ := Schedule(order, small); got != "a" {
			t.Fatalf("order %v: %s", order, got)
		}
	}
}

// A burst between two polls spreads: each placement reserves its share, so
// the next sees the node with less room.
func TestScheduleSpreadsWithReservations(t *testing.T) {
	cands := []Candidate{cand("a", 32768), cand("b", 32768), cand("c", 32768)}
	count := map[string]int{}
	for range 30 {
		name, err := Schedule(cands, small)
		if err != nil {
			t.Fatal(err)
		}
		count[name]++
		for i := range cands {
			if cands[i].Name == name {
				cands[i].Reserved.CPUs += small.CPUs
				cands[i].Reserved.MemoryMB += small.MemoryMB
			}
		}
	}
	if count["a"] != 10 || count["b"] != 10 || count["c"] != 10 {
		t.Fatalf("placements %v; want 10 each", count)
	}
}

func TestFallback(t *testing.T) {
	if name, ok := Fallback([]Candidate{cand("b", 1), cand("a", 1, func(c *Candidate) { c.Status.Cordoned = true })}, nil); !ok || name != "b" {
		t.Fatalf("fallback %q %v", name, ok)
	}
	if _, ok := Fallback([]Candidate{cand("a", 1)}, []string{"a"}); ok {
		t.Fatal("fallback to an excluded node")
	}
}

func TestCombineCapabilities(t *testing.T) {
	a := api.NodeStatus{Capabilities: api.Capabilities{Backend: "firecracker",
		Capabilities: map[string]bool{api.CapVolumes: true, api.CapSuspend: true},
		Limits:       api.Limits{MaxCPUs: 8, MaxMemoryMB: 8192, MaxDiskMB: 100, MaxIdleTimeoutSecs: 0},
		Network: api.NetworkCeiling{Ceiling: api.NetworkOpen, Default: api.NetworkPolicy{Mode: api.NetworkOpen},
			MayAllow: []string{"*"}}}}
	b := api.NodeStatus{Capabilities: api.Capabilities{Backend: "firecracker",
		Capabilities: map[string]bool{api.CapVolumes: true},
		Limits:       api.Limits{MaxCPUs: 4, MaxMemoryMB: 16384, MaxDiskMB: 200, MaxIdleTimeoutSecs: 3600},
		Network: api.NetworkCeiling{Ceiling: api.NetworkAllowlist, Default: api.NetworkPolicy{Mode: api.NetworkAllowlist},
			MayAllow: []string{"pypi.org"}}}}
	got := CombineCapabilities([]api.NodeStatus{a, b})
	if !got.Has(api.CapVolumes) || got.Has(api.CapSuspend) {
		t.Errorf("capabilities %v: want only what both have", got.Capabilities)
	}
	if got.Limits != (api.Limits{MaxCPUs: 4, MaxMemoryMB: 8192, MaxDiskMB: 100, MaxIdleTimeoutSecs: 3600}) {
		t.Errorf("limits %+v: want the smallest of each", got.Limits)
	}
	if got.Network.Ceiling != api.NetworkAllowlist || got.Network.Default.Mode != api.NetworkAllowlist {
		t.Errorf("network %+v: want the strictest", got.Network)
	}
	if len(got.Network.MayAllow) != 1 || got.Network.MayAllow[0] != "pypi.org" {
		t.Errorf("may_allow %v", got.Network.MayAllow)
	}
	if got.Backend != "firecracker" || got.APIVersion != api.Version {
		t.Errorf("backend %q version %q", got.Backend, got.APIVersion)
	}
	b.Capabilities.Backend = "macos"
	if got := CombineCapabilities([]api.NodeStatus{a, b}); got.Backend != "firecracker+macos" {
		t.Errorf("mixed backend %q", got.Backend)
	}
}

// A create in flight when a node's status is taken may or may not be in its
// Free. Counting it there and as a placement would refuse creates there is
// room for, so the last exact Free is kept until the node is quiet at a poll.
func TestPollKeepsFreeWhileACreateIsInFlight(t *testing.T) {
	tn := startNode(t, "n1", allCaps...)
	p := newNodePool(poolConfig{interval: time.Hour})
	n, err := p.add(tn.config(), true)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p.poll(ctx, n)
	c0, ok := n.candidate()
	if !ok {
		t.Fatal("the node is not healthy")
	}
	pl := n.reserve(api.NodeResources{CPUs: 1, MemoryMB: 1024})
	// The node changes under the in-flight create; the poll keeps Free.
	tn.capacity.MemoryMB -= 4096
	p.poll(ctx, n)
	c1, _ := n.candidate()
	if c1.Status.Free != c0.Status.Free || c1.Reserved.MemoryMB != 1024 {
		t.Fatalf("free %+v reserved %+v while a create is in flight", c1.Status.Free, c1.Reserved)
	}
	n.finish(pl, true)
	p.poll(ctx, n)
	c2, _ := n.candidate()
	if c2.Status.Free.MemoryMB != c0.Status.Free.MemoryMB-4096 || c2.Reserved.MemoryMB != 0 {
		t.Fatalf("after the create: free %+v reserved %+v", c2.Status.Free, c2.Reserved)
	}
	// A node never quiet has its Free taken after a few polls anyway.
	n.reserve(api.NodeResources{CPUs: 1, MemoryMB: 1024})
	tn.capacity.MemoryMB -= 1024
	for range maxHeldFree + 1 {
		p.poll(ctx, n)
	}
	if c3, _ := n.candidate(); c3.Status.Free.MemoryMB != c2.Status.Free.MemoryMB-1024 {
		t.Fatalf("free was held forever: %+v", c3.Status.Free)
	}
}
