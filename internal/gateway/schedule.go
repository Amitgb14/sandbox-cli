package gateway

import (
	"errors"
	"slices"
	"sort"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// The scheduler decides which node a new sandbox goes to. It is a pure
// function of what the nodes last reported and what the gateway has placed
// since, so it is tested without a node and gives the same answer for the
// same inputs.

// Candidate is one node as the scheduler sees it.
type Candidate struct {
	Name   string
	Status api.NodeStatus
	// Reserved is what the gateway has placed on the node since the status
	// was taken, which the status's Free does not yet show. Without it a
	// burst of creates between two polls would all see the same free node.
	Reserved api.NodeResources
}

// Want is what a create needs from a node.
type Want struct {
	Image    string // "" when the request names none: no pool or cache preference
	CPUs     float64
	MemoryMB int
	DiskMB   int
	// Caps are capabilities the node must have.
	Caps []string
	// NetworkMode, when set, must be within the node's ceiling.
	NetworkMode string
	// Node, when set, is the only node the sandbox may go to: the one holding
	// the snapshot or the volumes the request names.
	Node string
	// Exclude are nodes already tried for this request.
	Exclude []string
	// Spread, when set, counts what each node already holds of the
	// sandboxes this one is kept apart from (a service's replicas). Among the
	// nodes that fit, only those holding the fewest are chosen from: a node
	// that already holds one is avoided while another has room, so losing a
	// machine costs as few of them as it can.
	Spread map[string]int
}

// Errors from Schedule.
var (
	// errNoCandidate: no healthy, uncordoned node at all (or the pinned one is
	// not among them).
	errNoCandidate = errors.New("no node is taking new sandboxes")
	// errNoCapable: some node is up, but none can do what was asked. The
	// caller sends the request to a node anyway, so that the node — the
	// authority on what it can enforce — refuses it in its own words.
	errNoCapable = errors.New("no node has what this sandbox needs")
	// errNoRoom: nodes could run it, but none has the room now.
	errNoRoom = errors.New("no node has room for this sandbox")
)

// Schedule picks a node for w among cands, which the caller has already
// narrowed to healthy, uncordoned nodes. Among those that can run it and
// have room — and, with Spread, hold the fewest of its kind — it prefers, in
// order: one with a booted pool for the image, one
// with the image already built, the most free memory; ties go to the name
// that sorts first.
func Schedule(cands []Candidate, w Want) (string, error) {
	var live []Candidate
	for _, c := range cands {
		if c.Status.Cordoned || slices.Contains(w.Exclude, c.Name) {
			continue
		}
		if w.Node != "" && c.Name != w.Node {
			continue
		}
		live = append(live, c)
	}
	if len(live) == 0 {
		return "", errNoCandidate
	}
	var capable []Candidate
	for _, c := range live {
		if canRun(c.Status, w) {
			capable = append(capable, c)
		}
	}
	if len(capable) == 0 {
		return "", errNoCapable
	}
	var fits []Candidate
	for _, c := range capable {
		if hasRoom(c, w) {
			fits = append(fits, c)
		}
	}
	if len(fits) == 0 {
		return "", errNoRoom
	}
	if w.Spread != nil {
		least := w.Spread[fits[0].Name]
		for _, c := range fits {
			least = min(least, w.Spread[c.Name])
		}
		fits = slices.DeleteFunc(fits, func(c Candidate) bool { return w.Spread[c.Name] > least })
	}
	sort.SliceStable(fits, func(i, j int) bool { return better(fits[i], fits[j], w) })
	return fits[0].Name, nil
}

// Fallback picks the node a request goes to when no node can run it, so the
// node refuses it: among cands, the uncordoned one sorting first.
func Fallback(cands []Candidate, exclude []string) (string, bool) {
	names := []string{}
	for _, c := range cands {
		if !c.Status.Cordoned && !slices.Contains(exclude, c.Name) {
			names = append(names, c.Name)
		}
	}
	if len(names) == 0 {
		return "", false
	}
	slices.Sort(names)
	return names[0], true
}

func canRun(st api.NodeStatus, w Want) bool {
	for _, c := range w.Caps {
		if !st.Capabilities.Has(c) {
			return false
		}
	}
	if w.NetworkMode != "" && api.NetworkRank(w.NetworkMode) > api.NetworkRank(st.Capabilities.Network.Ceiling) {
		return false
	}
	l := st.Capabilities.Limits
	return w.CPUs <= l.MaxCPUs && w.MemoryMB <= l.MaxMemoryMB && w.DiskMB <= l.MaxDiskMB
}

func free(c Candidate) api.NodeResources {
	return api.NodeResources{
		CPUs:     c.Status.Free.CPUs - c.Reserved.CPUs,
		MemoryMB: c.Status.Free.MemoryMB - c.Reserved.MemoryMB,
		DiskMB:   c.Status.Free.DiskMB - c.Reserved.DiskMB,
	}
}

// hasRoom checks CPUs and memory always, and disk only on a node that
// reports a disk capacity: one that reports none has said nothing about its
// disk, and refusing on that would refuse every create.
func hasRoom(c Candidate, w Want) bool {
	f := free(c)
	if f.CPUs+1e-9 < w.CPUs || f.MemoryMB < w.MemoryMB {
		return false
	}
	return c.Status.Capacity.DiskMB == 0 || f.DiskMB >= w.DiskMB
}

func better(a, b Candidate, w Want) bool {
	if w.Image != "" {
		ap, bp := a.Status.Pooled[w.Image] > 0, b.Status.Pooled[w.Image] > 0
		if ap != bp {
			return ap
		}
		ac, bc := slices.Contains(a.Status.Images, w.Image), slices.Contains(b.Status.Images, w.Image)
		if ac != bc {
			return ac
		}
	}
	if fa, fb := free(a).MemoryMB, free(b).MemoryMB; fa != fb {
		return fa > fb
	}
	return a.Name < b.Name
}
