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
	spec    backend.Spec
	files   map[string]*node // absolute, cleaned path -> node
	procs   []*proc
	stopped bool
}

// New returns an empty fake with the capabilities given (api.Cap* names).
func New(caps ...string) *Backend {
	b := &Backend{sandboxes: map[string]*sandbox{}, caps: map[string]bool{}, snapshots: map[string]fakeSnapshot{}}
	for _, c := range caps {
		b.caps[c] = true
	}
	return b
}

func (b *Backend) Name() string { return "fake" }

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
	files := map[string]*node{"/": {dir: true}, "/tmp": {dir: true}, "/workspace": {dir: true}}
	if spec.FromSnapshot != "" {
		snap, ok := b.snapshots[spec.FromSnapshot]
		if !ok {
			return backend.ErrNotFound
		}
		files = copyFiles(snap.files)
	}
	b.sandboxes[spec.ID] = &sandbox{spec: spec, files: files}
	return nil
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
	_, err := b.get(id)
	return err
}

func (b *Backend) Resume(_ context.Context, id string) error {
	_, err := b.get(id)
	return err
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
	info := backend.SnapshotInfo{ID: snapshotID, Image: s.spec.Image, CPUs: s.spec.CPUs, MemoryMB: s.spec.MemoryMB, DiskMB: s.spec.DiskMB}
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
	s.mu.Unlock()
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
		cwd = "/workspace"
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
