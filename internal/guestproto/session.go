package guestproto

import (
	"bufio"
	"encoding/binary"
	"io"
	"os/exec"
	"sort"
	"sync"
)

// Kept processes (Request.Keep): a process that outlives the connection that
// started it, so a sandbox kept across a restart of sandboxd keeps its running
// commands, terminals and agent sessions too.
//
// Only the host asks for one, under an id it chooses. The guest buffers what
// the process writes — the newest keptBytes of it, so a process printing
// forever costs a bounded amount of the sandbox's own memory — and serves it
// to whichever connection attaches: from the offset the host already has,
// then live, then the exit code. One connection is attached at a time; a new
// attach takes over from the last. Once the exit code has been delivered the
// session is forgotten, and at most keptExited finished sessions are held for
// a host that has not come back for them.
//
// What the guest does not do is decide anything: it keeps what it was asked
// to keep and attaches only the id it is given. A kept process is ended by a
// signal from the host or by its VM ending, as any other.

const (
	keptBytes  = 1 << 20
	keptExited = 64
)

type keptFrame struct {
	typ  byte
	data []byte
	pos  int64 // stream position of data[0]
}

type session struct {
	id    string
	cmd   *exec.Cmd
	stdin io.WriteCloser
	term  *pty

	mu      sync.Mutex
	frames  []keptFrame
	bytes   int   // bytes held in frames
	total   int64 // bytes the process has written
	exit    *int
	done    uint64 // the order it finished in, for pruning the oldest first
	gen     int    // the attached connection's generation; a newer attach ends older ones
	changed chan struct{}
}

func (s *session) started() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd != nil
}

func (s *session) notify() { close(s.changed); s.changed = make(chan struct{}) }

// write buffers one chunk of output, dropping the oldest past keptBytes. It
// never blocks the process.
func (s *session) write(typ byte, p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames = append(s.frames, keptFrame{typ: typ, data: append([]byte(nil), p...), pos: s.total})
	s.total += int64(len(p))
	s.bytes += len(p)
	for s.bytes > keptBytes && len(s.frames) > 1 {
		s.bytes -= len(s.frames[0].data)
		s.frames = s.frames[1:]
	}
	s.notify()
}

func (s *session) finish(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exit = &code
	s.notify()
}

func (s *session) info() SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SessionInfo{ID: s.id, Running: s.exit == nil, ExitCode: s.exit}
}

// reserve claims a session id, or returns nil if it is taken.
func (s *Server) reserve(id string) *session {
	s.smu.Lock()
	defer s.smu.Unlock()
	if s.sessions == nil {
		s.sessions = map[string]*session{}
	}
	if _, taken := s.sessions[id]; taken {
		return nil
	}
	ks := &session{id: id, changed: make(chan struct{})}
	s.sessions[id] = ks
	return ks
}

// release gives back a reservation whose process never started.
func (s *Server) release(ks *session) {
	s.smu.Lock()
	defer s.smu.Unlock()
	if s.sessions[ks.id] == ks {
		delete(s.sessions, ks.id)
	}
}

// keep runs a started process as the kept session ks and serves it to c.
func (s *Server) keep(c io.ReadWriteCloser, br *bufio.Reader, ks *session, cmd *exec.Cmd, stdin io.WriteCloser, stdout, stderr io.Reader, term *pty) {
	ks.mu.Lock()
	ks.cmd, ks.stdin, ks.term = cmd, stdin, term
	ks.mu.Unlock()

	var outputs sync.WaitGroup
	copyOut := func(typ byte, r io.Reader) {
		defer outputs.Done()
		buf := make([]byte, 32<<10)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				ks.write(typ, buf[:n])
			}
			if err != nil {
				return
			}
		}
	}
	if term != nil {
		term.slave.Close() // the child holds its own; ours would keep the master from EOF
		outputs.Add(1)
		go copyOut(FrameStdout, stdout)
	} else {
		outputs.Add(2)
		go copyOut(FrameStdout, stdout)
		go copyOut(FrameStderr, stderr)
	}
	go func() {
		outputs.Wait()
		_ = cmd.Wait()
		if term != nil {
			term.close()
		}
		ks.finish(exitCode(cmd.ProcessState))
		s.smu.Lock()
		s.finished++
		ks.mu.Lock()
		ks.done = s.finished
		ks.mu.Unlock()
		s.smu.Unlock()
		s.pruneExited()
	}()
	s.serveSession(c, br, ks, 0)
}

// attach rejoins a kept session.
func (s *Server) attach(c io.ReadWriteCloser, br *bufio.Reader, req Request) {
	s.smu.Lock()
	ks := s.sessions[req.Session]
	s.smu.Unlock()
	if ks == nil || !ks.started() { // a reservation is not yet a process
		reply(c, fail(CodeNotFound, "no such session"))
		return
	}
	if req.Offset < 0 {
		reply(c, fail(CodeBadRequest, "offset must not be negative"))
		return
	}
	reply(c, Response{OK: true})
	s.serveSession(c, br, ks, req.Offset)
}

// serveSession attaches c to ks until c goes, a newer attach takes over, or
// the exit code has been delivered — which ends the session. Losing c only
// detaches: the process runs on.
func (s *Server) serveSession(c io.ReadWriteCloser, br *bufio.Reader, ks *session, offset int64) {
	ks.mu.Lock()
	ks.gen++
	gen := ks.gen
	ks.notify() // an older attach sees the new generation and lets go
	ks.mu.Unlock()

	var wmu sync.Mutex
	send := func(typ byte, b []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		return WriteFrame(c, typ, b)
	}
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		for {
			typ, payload, err := ReadFrame(br)
			if err != nil {
				return // detached; the process runs on
			}
			ks.mu.Lock()
			current := ks.gen == gen
			ks.mu.Unlock()
			if !current {
				return
			}
			switch typ {
			case FrameStdin:
				_, _ = ks.stdin.Write(payload)
			case FrameStdinEOF:
				if ks.term == nil {
					_ = ks.stdin.Close()
				} else {
					_, _ = ks.stdin.Write([]byte{4})
				}
			case FrameSignal:
				killGroup(ks.cmd, string(payload))
			case FrameResize:
				if ks.term != nil && len(payload) == 4 {
					ks.term.resize(binary.BigEndian.Uint16(payload[0:2]), binary.BigEndian.Uint16(payload[2:4]))
				}
			}
		}
	}()

	pos := offset
	first := true
	for {
		ks.mu.Lock()
		if ks.gen != gen {
			ks.mu.Unlock()
			return
		}
		var pending []keptFrame
		dropped := int64(0)
		if len(ks.frames) > 0 && pos < ks.frames[0].pos {
			dropped = ks.frames[0].pos - pos
		}
		for _, f := range ks.frames {
			if f.pos+int64(len(f.data)) <= pos {
				continue
			}
			if f.pos < pos { // a frame the host has part of
				f = keptFrame{typ: f.typ, data: f.data[pos-f.pos:], pos: pos}
			}
			pending = append(pending, f)
		}
		exit := ks.exit
		total := ks.total
		wait := ks.changed
		ks.mu.Unlock()

		if first && dropped > 0 {
			var b [8]byte
			binary.BigEndian.PutUint64(b[:], uint64(dropped))
			if send(FrameDropped, b[:]) != nil {
				return
			}
		}
		first = false
		for _, f := range pending {
			if send(f.typ, f.data) != nil {
				return
			}
		}
		pos = max(pos, total)
		if exit != nil {
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], uint32(int32(*exit)))
			if send(FrameExit, b[:]) == nil {
				s.forget(ks, gen)
			}
			return
		}
		select {
		case <-wait:
		case <-gone:
			return
		}
	}
}

// forget drops a session whose exit code was delivered, unless a newer attach
// has taken it over meanwhile.
func (s *Server) forget(ks *session, gen int) {
	ks.mu.Lock()
	current := ks.gen == gen
	ks.mu.Unlock()
	if !current {
		return
	}
	s.smu.Lock()
	if s.sessions[ks.id] == ks {
		delete(s.sessions, ks.id)
	}
	s.smu.Unlock()
}

// pruneExited keeps at most keptExited finished sessions, dropping those that
// finished first: a host that never comes back for them must not make the
// guest hold their output forever.
func (s *Server) pruneExited() {
	s.smu.Lock()
	defer s.smu.Unlock()
	var done []*session
	for _, ks := range s.sessions {
		ks.mu.Lock()
		if ks.exit != nil {
			done = append(done, ks)
		}
		ks.mu.Unlock()
	}
	if len(done) <= keptExited {
		return
	}
	sort.Slice(done, func(i, j int) bool { return done[i].done < done[j].done })
	for _, ks := range done[:len(done)-keptExited] {
		delete(s.sessions, ks.id)
	}
}

// listSessions answers OpSessions.
func (s *Server) listSessions(c io.Writer) {
	s.smu.Lock()
	out := make([]SessionInfo, 0, len(s.sessions))
	for _, ks := range s.sessions {
		if ks.started() {
			out = append(out, ks.info())
		}
	}
	s.smu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	reply(c, Response{OK: true, Sessions: out})
}
