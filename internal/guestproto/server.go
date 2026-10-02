package guestproto

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Server is the guest agent's side: it runs inside the VM and does what the host
// asks — run a process, read or write a file, list a directory — and nothing on
// its own initiative. It never opens a connection to the host; the host always
// dials it.
type Server struct {
	// Root is joined to every path. "/" in a real guest; a temporary directory in
	// tests, so the protocol can be exercised on the host.
	Root string
	// UID and GID are the identity processes run as, and that created files are
	// given, when the agent runs as root. -1 leaves them alone.
	UID, GID int
	// BaseEnv is the environment every process starts from; a request's env is
	// merged over it.
	BaseEnv map[string]string
}

// Serve answers connections from l until it is closed.
func (s *Server) Serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		go s.ServeConn(c)
	}
}

// ServeConn answers the one request a connection carries, then closes it.
func (s *Server) ServeConn(c io.ReadWriteCloser) {
	defer c.Close()
	br := bufio.NewReaderSize(c, 64<<10)
	line, err := readLine(br, MaxLine)
	if err != nil {
		return
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		reply(c, fail(CodeBadRequest, "unreadable request"))
		return
	}
	switch req.Op {
	case OpPing:
		reply(c, Response{OK: true, Version: Version})
	case OpRead:
		s.read(c, req)
	case OpWrite:
		s.write(c, br, req)
	case OpRemove:
		s.remove(c, req)
	case OpList:
		s.list(c, req)
	case OpExec:
		s.exec(c, br, req)
	case OpDial:
		s.dial(c, br, req)
	default:
		reply(c, fail(CodeBadRequest, "unknown op "+req.Op))
	}
}

func reply(w io.Writer, r Response) {
	b, _ := json.Marshal(r)
	_, _ = w.Write(append(b, '\n'))
}

func fail(code, msg string) Response { return Response{Error: &Error{Code: code, Message: msg}} }

// failErr maps a filesystem error onto a protocol code.
func failErr(err error) Response {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fail(CodeNotFound, "no such file or directory")
	case errors.Is(err, errIsDir):
		return fail(CodeIsDir, "is a directory")
	case isNotDir(err):
		return fail(CodeNotDir, "not a directory")
	case isNotEmpty(err):
		return fail(CodeNotEmpty, "directory not empty")
	}
	return fail(CodeInternal, err.Error())
}

var errIsDir = errors.New("is a directory")

// resolve validates a request path and maps it under Root.
func (s *Server) resolve(p string) (string, error) {
	if !strings.HasPrefix(p, "/") || strings.ContainsRune(p, 0) {
		return "", errors.New("path must be absolute")
	}
	for _, el := range strings.Split(p, "/") {
		if el == ".." {
			return "", errors.New("path must not contain ..")
		}
	}
	root := s.Root
	if root == "" {
		root = "/"
	}
	return filepath.Join(root, filepath.FromSlash(p)), nil
}

func (s *Server) read(w io.Writer, req Request) {
	p, err := s.resolve(req.Path)
	if err != nil {
		reply(w, fail(CodeBadRequest, err.Error()))
		return
	}
	f, err := os.Open(p)
	if err != nil {
		reply(w, failErr(err))
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		reply(w, failErr(err))
		return
	}
	if fi.IsDir() {
		reply(w, failErr(errIsDir))
		return
	}
	if fi.Size() > MaxFileBytes {
		reply(w, fail(CodeTooLarge, fmt.Sprintf("%d bytes exceeds %d", fi.Size(), MaxFileBytes)))
		return
	}
	// Read first, then announce: a file that grows between stat and read must not
	// make the announced size a lie.
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		reply(w, failErr(err))
		return
	}
	if len(data) > MaxFileBytes {
		reply(w, fail(CodeTooLarge, "file grew past the limit while being read"))
		return
	}
	reply(w, Response{OK: true, Size: int64(len(data))})
	_, _ = w.Write(data)
}

func (s *Server) write(w io.Writer, r io.Reader, req Request) {
	p, err := s.resolve(req.Path)
	if err != nil {
		reply(w, fail(CodeBadRequest, err.Error()))
		return
	}
	if req.Size < 0 || req.Size > MaxFileBytes {
		reply(w, fail(CodeTooLarge, fmt.Sprintf("%d bytes is outside 0..%d", req.Size, MaxFileBytes)))
		return
	}
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		reply(w, failErr(errIsDir))
		return
	}
	if err := s.mkdirAll(filepath.Dir(p)); err != nil {
		reply(w, failErr(err))
		return
	}
	reply(w, Response{OK: true}) // go ahead: send the bytes
	data := make([]byte, req.Size)
	if _, err := io.ReadFull(r, data); err != nil {
		return
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		reply(w, failErr(err))
		return
	}
	s.chown(p)
	reply(w, Response{OK: true})
}

// mkdirAll creates missing parents and gives each new one to the sandbox user.
func (s *Server) mkdirAll(dir string) error {
	if fi, err := os.Stat(dir); err == nil {
		if !fi.IsDir() {
			return fmt.Errorf("%s: %w", dir, errNotDirSentinel)
		}
		return nil
	}
	if err := s.mkdirAll(filepath.Dir(dir)); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	s.chown(dir)
	return nil
}

var errNotDirSentinel = errors.New("not a directory")

func isNotDir(err error) bool {
	return errors.Is(err, errNotDirSentinel) || errors.Is(err, errnoNotDir)
}

func isNotEmpty(err error) bool { return errors.Is(err, errnoNotEmpty) }

func (s *Server) remove(w io.Writer, req Request) {
	p, err := s.resolve(req.Path)
	if err != nil {
		reply(w, fail(CodeBadRequest, err.Error()))
		return
	}
	if err := os.Remove(p); err != nil {
		reply(w, failErr(err))
		return
	}
	reply(w, Response{OK: true})
}

func (s *Server) list(w io.Writer, req Request) {
	p, err := s.resolve(req.Path)
	if err != nil {
		reply(w, fail(CodeBadRequest, err.Error()))
		return
	}
	fi, err := os.Stat(p)
	if err != nil {
		reply(w, failErr(err))
		return
	}
	if !fi.IsDir() {
		reply(w, fail(CodeNotDir, "not a directory"))
		return
	}
	des, err := os.ReadDir(p)
	if err != nil {
		reply(w, failErr(err))
		return
	}
	out := []DirEntry{}
	for _, de := range des {
		e := DirEntry{Name: de.Name(), Type: "other"}
		switch {
		case de.Type()&fs.ModeSymlink != 0:
			e.Type = "symlink"
		case de.IsDir():
			e.Type = "dir"
		case de.Type().IsRegular():
			e.Type = "file"
			if info, err := de.Info(); err == nil {
				e.Size = info.Size()
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	reply(w, Response{OK: true, Entries: out})
}

// exec runs a process and streams it. The process ends with the connection: a
// host that went away does not leave a process running in its sandbox.
func (s *Server) exec(c io.ReadWriteCloser, br *bufio.Reader, req Request) {
	if len(req.Argv) == 0 || req.Argv[0] == "" {
		reply(c, fail(CodeBadRequest, "argv is required"))
		return
	}
	cwd := "/"
	if req.Cwd != "" {
		cwd = req.Cwd
	}
	dir, err := s.resolve(cwd)
	if err != nil {
		reply(c, fail(CodeBadRequest, "cwd: "+err.Error()))
		return
	}

	env := map[string]string{}
	for k, v := range s.BaseEnv {
		env[k] = v
	}
	for k, v := range req.Env {
		env[k] = v
	}
	// Resolve argv[0] against the process's own PATH, not this agent's.
	name := req.Argv[0]
	if !strings.ContainsRune(name, '/') {
		found, err := lookPath(name, env["PATH"], s.Root)
		if err != nil {
			reply(c, fail(CodeNoSuchCommand, "no such command: "+name))
			return
		}
		name = found
	}
	cmd := exec.Command(name, req.Argv[1:]...)
	cmd.Args[0] = req.Argv[0]
	cmd.Dir = dir
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.SysProcAttr = s.procAttr()
	var stdin io.WriteCloser
	var stdout, stderr io.Reader
	var term *pty
	if req.Tty {
		t, err := openPTY(req.Rows, req.Cols)
		if err != nil {
			reply(c, fail(CodeInternal, "allocating a terminal: "+err.Error()))
			return
		}
		term = t
		defer term.close()
		cmd.Stdin, cmd.Stdout, cmd.Stderr = term.slave, term.slave, term.slave
		ttyAttr(cmd.SysProcAttr)
		stdin, stdout = term.master, term.master
		if cmd.Env != nil && env["TERM"] == "" {
			cmd.Env = append(cmd.Env, "TERM=xterm-256color")
		}
	} else {
		stdin, _ = cmd.StdinPipe()
		stdout, _ = cmd.StdoutPipe()
		stderr, _ = cmd.StderrPipe()
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			reply(c, fail(CodeNoSuchCommand, "no such command: "+req.Argv[0]))
		} else {
			reply(c, fail(CodeInternal, err.Error()))
		}
		return
	}
	reply(c, Response{OK: true})

	var wmu sync.Mutex
	send := func(typ byte, b []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		return WriteFrame(c, typ, b)
	}
	var outputs sync.WaitGroup
	copyOut := func(typ byte, r io.Reader) {
		defer outputs.Done()
		buf := make([]byte, 32<<10)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				if send(typ, buf[:n]) != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}
	if term != nil {
		// The child holds its own copy of the slave; ours must go, or the master
		// never sees EOF when the process exits.
		term.slave.Close()
		outputs.Add(1)
		go copyOut(FrameStdout, stdout)
	} else {
		outputs.Add(2)
		go copyOut(FrameStdout, stdout)
		go copyOut(FrameStderr, stderr)
	}

	// Frames from the host. When the connection drops, the process is killed:
	// nobody is left to read its output or to stop it.
	go func() {
		for {
			typ, payload, err := ReadFrame(br)
			if err != nil {
				killGroup(cmd, "KILL")
				return
			}
			switch typ {
			case FrameStdin:
				_, _ = stdin.Write(payload)
			case FrameStdinEOF:
				if term == nil {
					_ = stdin.Close()
				} else {
					_, _ = stdin.Write([]byte{4}) // ^D: a terminal has no EOF of its own
				}
			case FrameSignal:
				killGroup(cmd, string(payload))
			case FrameResize:
				if term != nil && len(payload) == 4 {
					term.resize(binary.BigEndian.Uint16(payload[0:2]), binary.BigEndian.Uint16(payload[2:4]))
				}
			}
		}
	}()

	outputs.Wait()
	_ = cmd.Wait()
	code := exitCode(cmd.ProcessState)
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(int32(code)))
	_ = send(FrameExit, b[:])
}

// dial connects to a port on the guest's own loopback — never anywhere else:
// a tunnel reaches a server in the sandbox, it is not a way around the
// sandbox's egress policy.
func (s *Server) dial(c io.ReadWriteCloser, br *bufio.Reader, req Request) {
	if req.Port < 1 || req.Port > 65535 {
		reply(c, fail(CodeBadRequest, "port must be 1-65535"))
		return
	}
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(req.Port)))
	if err != nil {
		reply(c, fail(CodeNotFound, "nothing is listening on that port"))
		return
	}
	defer conn.Close()
	reply(c, Response{OK: true})
	// Plain Reader/Writer wrappers on purpose: io.Copy would otherwise take the
	// destination's ReadFrom, and *os.File's (the vsock connection) splices —
	// which stalls on a blocking vsock descriptor the runtime's poller does not
	// manage. Found by the tunnel conformance test hanging on a real guest.
	// Each direction ends with a half-close passed on, so a client that sends
	// and then waits for EOF (most of them) gets its whole reply. The guest's
	// side finishing ends the tunnel; the host finishing only stops input.
	go func() {
		_, _ = io.Copy(struct{ io.Writer }{conn}, struct{ io.Reader }{br})
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()
	_, _ = io.Copy(struct{ io.Writer }{c}, struct{ io.Reader }{conn})
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
}

// lookPath finds name on a PATH, inside root.
func lookPath(name, path, root string) (string, error) {
	if root == "" {
		root = "/"
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		p := filepath.Join(root, dir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fs.ErrNotExist
}
