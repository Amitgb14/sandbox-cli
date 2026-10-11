package gateway

import (
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// A thousand creates at once, through one gateway, across three nodes: all
// succeed, every one is recorded, and they spread. The scheduler's
// reservations are what spread them — between two polls every node reports
// the same free room, and without them the burst would all go to one.
//
// Skipped under -short.
func TestLoadThousandConcurrentCreates(t *testing.T) {
	if testing.Short() {
		t.Skip("load test; run without -short")
	}
	const total = 1000
	var nodes []*testNode
	for _, name := range []string{"n1", "n2", "n3"} {
		n := startNode(t, name, allCaps...)
		n.capacity = api.NodeResources{CPUs: 400, MemoryMB: 400 << 10, DiskMB: 1 << 30}
		nodes = append(nodes, n)
	}
	// A node's last exact Free is held for maxHeldFree polls while creates
	// are in flight, then taken anyway, counting those twice. At the
	// harness's 100ms that is one second, which a burst under -race on a
	// shared runner outlasts, and the nodes' last 70-odd slots each look
	// taken: a few creates were refused "no node has room". A one-second
	// poll gives the burst ten, as the default five-second poll gives a
	// real one fifty. What this test checks, that reservations spread a
	// burst between two polls, is the same either way.
	tg := startGateway(t, func(c *Config) {
		c.Quota = Quota{Sandboxes: total}
		c.PollInterval = time.Second
	}, nodes...)
	secret, _, err := tg.store.CreateKey("load", "bench", []string{ScopeCreate, ScopeRead})
	if err != nil {
		t.Fatal(err)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = total
	c := api.NewClientWithHTTP(tg.ts.URL, secret, &http.Client{Transport: tr})

	lat := make([]time.Duration, total)
	ids := make([]string, total)
	errs := make([]error, total)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range total {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			t0 := time.Now()
			sb, err := c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{})
			lat[i], ids[i], errs[i] = time.Since(t0), sb.ID, err
		}()
	}
	t0 := time.Now()
	close(start)
	wg.Wait()
	wall := time.Since(t0)

	failed := 0
	for i, err := range errs {
		if err != nil {
			if failed < 5 {
				t.Errorf("create %d: %v", i, err)
			}
			failed++
		}
	}
	if failed > 0 {
		t.Fatalf("%d of %d creates failed", failed, total)
	}
	per := map[string]int{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("id %s returned twice", id)
		}
		seen[id] = true
		o, ok := tg.store.OwnerOf(id)
		if !ok || o.User != "load" || o.Tenant != "bench" {
			t.Fatalf("%s: owner %+v %v", id, o, ok)
		}
		per[o.Node]++
	}
	got := 0
	for _, n := range nodes {
		got += n.count()
		if per[n.name] < total/5 {
			t.Errorf("node %s got %d of %d: not spread", n.name, per[n.name], total)
		}
	}
	if got != total {
		t.Fatalf("the nodes hold %d sandboxes, want %d", got, total)
	}
	if u := tg.store.UsageOf("bench"); u.Sandboxes != total {
		t.Fatalf("usage %+v", u)
	}
	// The quota held: one more is refused.
	_, err = c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{})
	wantCode(t, err, api.CodeRefused)

	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	t.Logf("%d concurrent creates through the gateway across 3 nodes: wall %v (%.0f/s); latency p50 %v p90 %v p99 %v max %v; per node %v",
		total, wall.Round(time.Millisecond), float64(total)/wall.Seconds(),
		lat[total/2].Round(time.Millisecond), lat[total*9/10].Round(time.Millisecond),
		lat[total*99/100].Round(time.Millisecond), lat[total-1].Round(time.Millisecond), per)
}
