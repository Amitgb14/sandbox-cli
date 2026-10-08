// Package fake is an in-memory backend, so the API, the policy and the server
// can be tested — and the conformance suite run — with no VM and no daemon.
//
// It never starts a host process. Commands are a handful of builtins chosen
// because every Linux image has them with the same behaviour: echo, cat,
// printenv, pwd, sleep, true and false. A conformance test written with only
// these runs unchanged against a real VM, which is the point of the suite; a
// test that needs anything else belongs in docs/testing/end-to-end.md instead.
//
// What it cannot prove is everything that makes a sandbox a sandbox: that a VM
// booted, that egress was dropped, that the guest could not reach the host. It
// records the network policy it is given and enforces none of it.
package fake

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
)

// Backend is the fake. The zero value is not usable; call New.
type Backend struct {
	mu        sync.Mutex
	sandboxes map[string]*sandbox
	caps      map[string]bool
	snapshots map[string]fakeSnapshot
	volumes   map[string]*fakeVolume
	images    map[string]bool // every image a sandbox was created from
}

// fakeVolume holds a volume's files by path relative to its mount point. A
// sandbox gets a copy at create and hands its changes back at terminate,
// which is what a real one looks like from outside: what the last sandbox
// left is what the next one finds.
type fakeVolume struct {
	info  backend.VolumeInfo
	files map[string]*node
}

type fakeSnapshot struct {
	info  backend.SnapshotInfo
	files map[string]*node
}

type node struct {
	dir  bool
	data []byte
}

type sandbox struct {
	mu      sync.Mutex
	used    int64 // the fake's CPU time, microseconds: grows with each reading
	spec    backend.Spec
	files   map[string]*node // absolute, cleaned path -> node
	procs   []*proc
	stopped bool
	// suspended is only what Kept reports; the fake runs nothing either way.
	suspended bool
}

// New returns an empty fake with the capabilities given (api.Cap* names).
func New(caps ...string) *Backend {
	b := &Backend{sandboxes: map[string]*sandbox{}, caps: map[string]bool{}, snapshots: map[string]fakeSnapshot{},
		volumes: map[string]*fakeVolume{}, images: map[string]bool{}}
	for _, c := range caps {
		b.caps[c] = true
	}
	return b
}

func (b *Backend) Name() string { return "fake" }

// Count is how many sandboxes the fake holds, for tests.
func (b *Backend) Count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.sandboxes)
}

// IDs is the set of sandboxes the fake holds, for tests.
func (b *Backend) IDs() map[string]bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]bool{}
	for id := range b.sandboxes {
		out[id] = true
	}
	return out
}

func (b *Backend) Capabilities() map[string]bool {
	out := map[string]bool{}
	for k, v := range b.caps {
		out[k] = v
	}
	return out
}

// Network returns the policy last given to a sandbox, so tests can check what
// would have been enforced.
func (b *Backend) Network(id string) (api.NetworkPolicy, bool) {
	s, err := b.get(id)
	if err != nil {
		return api.NetworkPolicy{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spec.Network, true
}

func (b *Backend) get(id string) (*sandbox, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.sandboxes[id]
	if !ok {
		return nil, backend.ErrNotFound
	}
	return s, nil
}

func (b *Backend) Create(_ context.Context, spec backend.Spec) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, dup := b.sandboxes[spec.ID]; dup {
		return fmt.Errorf("fake: sandbox %s already exists", spec.ID)
	}
	files := map[string]*node{"/": {dir: true}, "/tmp": {dir: true}, "/sandbox": {dir: true}, "/sandbox/home": {dir: true}}
	if spec.FromSnapshot != "" {
		snap, ok := b.snapshots[spec.FromSnapshot]
		if !ok {
			return backend.ErrNotFound
		}
		files = copyFiles(snap.files)
	}
	for _, m := range spec.Volumes {
		v, ok := b.volumes[m.Name]
		if !ok {
			return backend.ErrNotFound
		}
		for dir := m.Path; dir != "/"; dir = path.Dir(dir) {
			files[dir] = &node{dir: true}
		}
		for rel, n := range v.files {
			files[path.Join(m.Path, rel)] = &node{dir: n.dir, data: append([]byte(nil), n.data...)}
		}
	}
	b.sandboxes[spec.ID] = &sandbox{spec: spec, files: files}
	if spec.FromSnapshot == "" {
		b.images[spec.Image] = true
	}
	return nil
}

// readOnly reports whether p is under one of the sandbox's read-only mounts.
// Caller holds s.mu.
func (s *sandbox) readOnly(p string) bool {
	for _, m := range s.spec.Volumes {
		if m.ReadOnly && (p == m.Path || strings.HasPrefix(p, m.Path+"/")) {
			return true
		}
	}
	return false
}

// CachedImages is backend.ImageLister: the fake "builds" an image's disk the
// first time a sandbox is created from it.
func (b *Backend) CachedImages() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.images))
	for img := range b.images {
		out = append(out, img)
	}
	return out
}

func (b *Backend) CreateVolume(_ context.Context, name string, sizeMB int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.volumes[name]; ok {
		return fmt.Errorf("fake: volume %s exists", name)
	}
	b.volumes[name] = &fakeVolume{info: backend.VolumeInfo{Name: name, SizeMB: sizeMB, CreatedAt: time.Now().UTC()}, files: map[string]*node{}}
	return nil
}

func (b *Backend) DeleteVolume(_ context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.volumes[name]; !ok {
		return backend.ErrNotFound
	}
	delete(b.volumes, name)
	return nil
}

func (b *Backend) Volumes(context.Context) ([]backend.VolumeInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]backend.VolumeInfo, 0, len(b.volumes))
	for _, v := range b.volumes {
		out = append(out, v.info)
	}
	return out, nil
}

func copyFiles(in map[string]*node) map[string]*node {
	out := make(map[string]*node, len(in))
	for p, n := range in {
		out[p] = &node{dir: n.dir, data: append([]byte(nil), n.data...)}
	}
	return out
}

// Suspend and Resume record nothing the fake could lose: its sandboxes have no
// memory to keep. They exist so the server's handling of the states is tested.
func (b *Backend) Suspend(_ context.Context, id string) error {
	s, err := b.get(id)
	if err == nil {
		s.mu.Lock()
		s.suspended = true
		s.mu.Unlock()
	}
	return err
}

func (b *Backend) Resume(_ context.Context, id string) error {
	s, err := b.get(id)
	if err == nil {
		s.mu.Lock()
		s.suspended = false
		s.mu.Unlock()
	}
	return err
}

// Kept is backend.Keeper: the fake outlives a server in a test the way a VM
// outlives sandboxd, so every sandbox it holds is one a new server can take
// back.
func (b *Backend) Kept() map[string]backend.Kept {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]backend.Kept{}
	for id, s := range b.sandboxes {
		s.mu.Lock()
		out[id] = backend.Kept{Suspended: s.suspended}
		s.mu.Unlock()
	}
	return out
}

// Detach is backend.Keeper. The sandboxes stay; their processes end, as a
// guest agent ends a process whose host connection is gone.
func (b *Backend) Detach() {
	b.mu.Lock()
	var procs []*proc
	for _, s := range b.sandboxes {
		s.mu.Lock()
		procs = append(procs, s.procs...)
		s.procs = nil
		s.mu.Unlock()
	}
	b.mu.Unlock()
	for _, p := range procs {
		_ = p.Signal("KILL")
	}
}

// Snapshot captures the files; a sandbox started from it gets its own copy, so
// the two diverge from there — which is what a fork means.
func (b *Backend) Snapshot(_ context.Context, id, snapshotID string) (backend.SnapshotInfo, error) {
	s, err := b.get(id)
	if err != nil {
		return backend.SnapshotInfo{}, err
	}
	s.mu.Lock()
	files := copyFiles(s.files)
	// The fake keeps files only, so it is whichever kind it was told it has.
	kind := api.SnapshotDisk
	if b.caps[api.CapMemorySnapshot] {
		kind = api.SnapshotMemory
	}
	info := backend.SnapshotInfo{ID: snapshotID, Kind: kind, Image: s.spec.Image, CPUs: s.spec.CPUs, MemoryMB: s.spec.MemoryMB, DiskMB: s.spec.DiskMB}
	s.mu.Unlock()
	b.mu.Lock()
	b.snapshots[snapshotID] = fakeSnapshot{info: info, files: files}
	b.mu.Unlock()
	return info, nil
}

func (b *Backend) DeleteSnapshot(_ context.Context, snapshotID string) error {
	b.mu.Lock()
	delete(b.snapshots, snapshotID)
	b.mu.Unlock()
	return nil
}

func (b *Backend) UpdateNetwork(_ context.Context, id string, p api.NetworkPolicy) error {
	s, err := b.get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spec.Network = p
	return nil
}

func (b *Backend) Terminate(_ context.Context, id string) error {
	b.mu.Lock()
	s, ok := b.sandboxes[id]
	delete(b.sandboxes, id)
	b.mu.Unlock()
	if !ok {
		return nil
	}
	s.mu.Lock()
	s.stopped = true
	procs := append([]*proc(nil), s.procs...)
	// What the sandbox left under each writable mount becomes the volume.
	saved := map[string]map[string]*node{}
	for _, m := range s.spec.Volumes {
		if m.ReadOnly {
			continue
		}
		files := map[string]*node{}
		for p, n := range s.files {
			if strings.HasPrefix(p, m.Path+"/") {
				files[strings.TrimPrefix(p, m.Path+"/")] = n
			}
		}
		saved[m.Name] = files
	}
	s.mu.Unlock()
	b.mu.Lock()
	for name, files := range saved {
		if v, ok := b.volumes[name]; ok {
			v.files = files
		}
	}
	b.mu.Unlock()
	for _, p := range procs {
		_ = p.Signal("KILL")
	}
	return nil
}

// --- files -----------------------------------------------------------------

func (b *Backend) ReadFile(_ context.Context, id, p string) ([]byte, error) {
	s, err := b.get(id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(p)
}

func (s *sandbox) read(p string) ([]byte, error) {
	n, ok := s.files[path.Clean(p)]
	switch {
	case !ok:
		return nil, backend.ErrNotFound
	case n.dir:
		return nil, backend.ErrIsDir
	}
	return append([]byte(nil), n.data...), nil
}

func (b *Backend) WriteFile(_ context.Context, id, p string, data []byte) error {
	s, err := b.get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p = path.Clean(p)
	if s.readOnly(p) {
		return backend.ErrReadOnly
	}
	if n, ok := s.files[p]; ok && n.dir {
		return backend.ErrIsDir
	}
	// Parents are created, as the API promises; a parent that is a file is not.
	for dir := path.Dir(p); ; dir = path.Dir(dir) {
		if n, ok := s.files[dir]; ok {
			if !n.dir {
				return backend.ErrNotDir
			}
		} else {
			s.files[dir] = &node{dir: true}
		}
		if dir == "/" {
			break
		}
	}
	s.files[p] = &node{data: append([]byte(nil), data...)}
	return nil
}

func (b *Backend) Remove(_ context.Context, id, p string) error {
	s, err := b.get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p = path.Clean(p)
	if s.readOnly(p) {
		return backend.ErrReadOnly
	}
	n, ok := s.files[p]
	if !ok {
		return backend.ErrNotFound
	}
	if n.dir {
		if len(s.children(p)) > 0 {
			return backend.ErrNotEmpty
		}
	}
	delete(s.files, p)
	return nil
}

func (b *Backend) ListDir(_ context.Context, id, p string) ([]api.DirEntry, error) {
	s, err := b.get(id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p = path.Clean(p)
	n, ok := s.files[p]
	switch {
	case !ok:
		return nil, backend.ErrNotFound
	case !n.dir:
		return nil, backend.ErrNotDir
	}
	out := []api.DirEntry{}
	for _, name := range s.children(p) {
		c := s.files[path.Join(p, name)]
		e := api.DirEntry{Name: name, Type: "file", Size: int64(len(c.data))}
		if c.dir {
			e.Type, e.Size = "dir", 0
		}
		out = append(out, e)
	}
	return out, nil
}

// children lists the direct children of dir by name, sorted. Caller holds s.mu.
func (s *sandbox) children(dir string) []string {
	var out []string
	for p := range s.files {
		if p != dir && path.Dir(p) == dir {
			out = append(out, path.Base(p))
		}
	}
	sort.Strings(out)
	return out
}

// --- processes -------------------------------------------------------------

type proc struct {
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	signals chan int
	done    chan struct{}
	code    int
}

var signalNumbers = map[string]int{"HUP": 1, "INT": 2, "KILL": 9, "TERM": 15}

func (p *proc) Stdin() io.WriteCloser { return p.stdinW }

func (p *proc) Signal(sig string) error {
	n, ok := signalNumbers[sig]
	if !ok {
		return backend.ErrBadSignal
	}
	select {
	case p.signals <- n:
	case <-p.done: // already exited: delivering to a dead process is not an error
	}
	return nil
}

func (p *proc) Wait() int {
	<-p.done
	return p.code
}

var builtins = map[string]bool{
	"echo": true, "cat": true, "printenv": true, "pwd": true,
	"sleep": true, "true": true, "false": true,
}

func (b *Backend) Start(_ context.Context, id string, ps backend.ProcSpec, stdout, stderr io.Writer) (backend.Proc, error) {
	s, err := b.get(id)
	if err != nil {
		return nil, err
	}
	if len(ps.Argv) == 0 || !builtins[ps.Argv[0]] {
		return nil, backend.ErrNoSuchCmd
	}
	r, w := io.Pipe()
	p := &proc{stdinR: r, stdinW: w, signals: make(chan int, 1), done: make(chan struct{})}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil, backend.ErrNotFound
	}
	if ps.Cwd != "" {
		if n, ok := s.files[path.Clean(ps.Cwd)]; !ok || !n.dir {
			s.mu.Unlock()
			return nil, backend.ErrNoSuchCwd
		}
	}
	s.procs = append(s.procs, p)
	s.mu.Unlock()

	go func() {
		p.code = s.exec(p, ps, stdout, stderr)
		_ = r.Close()
		close(p.done)
	}()
	return p, nil
}

// exec runs one builtin and returns its exit code.
func (s *sandbox) exec(p *proc, ps backend.ProcSpec, stdout, stderr io.Writer) int {
	args := ps.Argv[1:]
	cwd := ps.Cwd
	if cwd == "" {
		cwd = "/sandbox/home"
	}
	switch ps.Argv[0] {
	case "true":
		return 0
	case "false":
		return 1
	case "pwd":
		fmt.Fprintln(stdout, cwd)
		return 0
	case "echo":
		fmt.Fprintln(stdout, strings.Join(args, " "))
		return 0
	case "printenv":
		if len(args) == 0 {
			keys := make([]string, 0, len(ps.Env))
			for k := range ps.Env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(stdout, "%s=%s\n", k, ps.Env[k])
			}
			return 0
		}
		v, ok := ps.Env[args[0]]
		if !ok {
			return 1
		}
		fmt.Fprintln(stdout, v)
		return 0
	case "sleep":
		secs, err := strconv.ParseFloat(firstOr(args, ""), 64)
		if err != nil || secs < 0 {
			fmt.Fprintln(stderr, "sleep: invalid time interval")
			return 1
		}
		select {
		case <-time.After(time.Duration(secs * float64(time.Second))):
			return 0
		case n := <-p.signals:
			return 128 + n
		}
	case "cat":
		if len(args) == 0 {
			return copyStdin(p, stdout)
		}
		code := 0
		for _, a := range args {
			if !path.IsAbs(a) {
				a = path.Join(cwd, a)
			}
			s.mu.Lock()
			data, err := s.read(a)
			s.mu.Unlock()
			if err != nil {
				fmt.Fprintf(stderr, "cat: %s: %v\n", a, err)
				code = 1
				continue
			}
			_, _ = stdout.Write(data)
		}
		return code
	}
	return 127
}

// copyStdin is `cat` with no arguments: stdin to stdout until EOF, or until a
// signal ends it.
func copyStdin(p *proc, stdout io.Writer) int {
	chunks := make(chan []byte)
	quit := make(chan struct{})
	defer close(quit) // releases the reader if a signal ended the copy first
	go func() {
		defer close(chunks)
		buf := make([]byte, 32<<10)
		for {
			n, err := p.stdinR.Read(buf)
			if n > 0 {
				select {
				case chunks <- bytes.Clone(buf[:n]):
				case <-quit:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case c, ok := <-chunks:
			if !ok {
				return 0
			}
			_, _ = stdout.Write(c)
		case n := <-p.signals:
			_ = p.stdinR.Close()
			return 128 + n
		}
	}
}

func firstOr(s []string, def string) string {
	if len(s) == 0 {
		return def
	}
	return s[0]
}

// Usage is backend.UsageReader with made-up but steady numbers: no VM runs
// here, so there is nothing to measure, and the server's sampling and the
// API's shape still need something to carry. CPU time grows by 2% of one
// vCPU-second per reading per running process, memory is 64 MiB and 8 MiB
// a process, within the sandbox's memory.
func (b *Backend) Usage(context.Context) (map[string]backend.Usage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]backend.Usage{}
	for id, s := range b.sandboxes {
		s.mu.Lock()
		if !s.stopped {
			running := 0
			for _, p := range s.procs {
				select {
				case <-p.done:
				default:
					running++
				}
			}
			s.used += int64(20_000 * (1 + running))
			mem := int64(64+8*running) << 20
			limit := int64(s.spec.MemoryMB) << 20
			if limit > 0 && mem > limit {
				mem = limit
			}
			out[id] = backend.Usage{CPUUsec: s.used, MemoryBytes: mem, MemoryLimitBytes: limit, Processes: 1 + running}
		}
		s.mu.Unlock()
	}
	return out, nil
}
