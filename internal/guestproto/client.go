package guestproto

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// Dialer opens one connection to the guest agent. The client opens a fresh one
// per request, so a dialer is all a backend has to provide.
type Dialer func(ctx context.Context) (io.ReadWriteCloser, error)

// Client speaks to one guest.
type Client struct {
	Dial Dialer
}

// conn is one request's connection.
type conn struct {
	rwc io.ReadWriteCloser
	br  *bufio.Reader
}

// open dials and sends the request line. The connection is closed when ctx ends,
// which is what bounds a guest that stops answering.
func (c *Client) open(ctx context.Context, req Request) (*conn, func(), error) {
	rwc, err := c.Dial(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("dialling the guest agent: %w", err)
	}
	stop := context.AfterFunc(ctx, func() { rwc.Close() })
	cleanup := func() { stop(); rwc.Close() }
	line, err := json.Marshal(req)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if _, err := rwc.Write(append(line, '\n')); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("sending to the guest agent: %w", err)
	}
	return &conn{rwc: rwc, br: bufio.NewReaderSize(rwc, 64<<10)}, cleanup, nil
}

// response reads one answer line, bounded by MaxLine.
func (cn *conn) response() (Response, error) {
	var resp Response
	line, err := readLine(cn.br, MaxLine)
	if err != nil {
		return resp, fmt.Errorf("reading the guest agent's answer: %w", err)
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return resp, fmt.Errorf("the guest agent sent an unreadable answer: %w", err)
	}
	if !resp.OK {
		if resp.Error == nil {
			return resp, &Error{Code: CodeInternal, Message: "the guest agent failed without saying why"}
		}
		return resp, resp.Error
	}
	return resp, nil
}

// readLine reads up to and excluding '\n', refusing a line longer than max.
func readLine(br *bufio.Reader, max int) ([]byte, error) {
	var out []byte
	for {
		chunk, err := br.ReadSlice('\n')
		out = append(out, chunk...)
		if len(out) > max {
			return nil, fmt.Errorf("line exceeds %d bytes", max)
		}
		if err == nil {
			return out[:len(out)-1], nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
}

// Ping returns the guest agent's protocol version.
func (c *Client) Ping(ctx context.Context) (string, error) {
	cn, done, err := c.open(ctx, Request{Op: OpPing})
	if err != nil {
		return "", err
	}
	defer done()
	resp, err := cn.response()
	return resp.Version, err
}

// WaitReady pings until the guest agent answers or ctx ends. A VM that is
// booting refuses connections for a few milliseconds; that is not an error.
func (c *Client) WaitReady(ctx context.Context) error {
	for {
		pctx, cancel := context.WithTimeout(ctx, time.Second)
		_, err := c.Ping(pctx)
		cancel()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the guest agent did not answer: %w (last error: %v)", ctx.Err(), err)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// ReadFile returns a guest file's contents.
func (c *Client) ReadFile(ctx context.Context, path string) ([]byte, error) {
	cn, done, err := c.open(ctx, Request{Op: OpRead, Path: path})
	if err != nil {
		return nil, err
	}
	defer done()
	resp, err := cn.response()
	if err != nil {
		return nil, err
	}
	if resp.Size < 0 || resp.Size > MaxFileBytes {
		return nil, fmt.Errorf("the guest agent announced %d bytes; the limit is %d", resp.Size, MaxFileBytes)
	}
	buf := make([]byte, resp.Size)
	if _, err := io.ReadFull(cn.br, buf); err != nil {
		return nil, fmt.Errorf("reading file contents: %w", err)
	}
	return buf, nil
}

// WriteFile writes a guest file, creating parent directories.
func (c *Client) WriteFile(ctx context.Context, path string, data []byte) error {
	if len(data) > MaxFileBytes {
		return &Error{Code: CodeTooLarge, Message: fmt.Sprintf("%d bytes exceeds %d", len(data), MaxFileBytes)}
	}
	cn, done, err := c.open(ctx, Request{Op: OpWrite, Path: path, Size: int64(len(data))})
	if err != nil {
		return err
	}
	defer done()
	// The guest checks the path before accepting the bytes, so a refusal arrives
	// without the data being sent.
	if _, err := cn.response(); err != nil {
		return err
	}
	if _, err := cn.rwc.Write(data); err != nil {
		return fmt.Errorf("sending file contents: %w", err)
	}
	_, err = cn.response()
	return err
}

// Remove removes a guest file or empty directory.
func (c *Client) Remove(ctx context.Context, path string) error {
	cn, done, err := c.open(ctx, Request{Op: OpRemove, Path: path})
	if err != nil {
		return err
	}
	defer done()
	_, err = cn.response()
	return err
}

// Sync flushes the guest's filesystems to disk. final says the VM is about to
// stop for good (see Request.Final).
func (c *Client) Sync(ctx context.Context, final bool) error {
	cn, done, err := c.open(ctx, Request{Op: OpSync, Final: final})
	if err != nil {
		return err
	}
	defer done()
	_, err = cn.response()
	return err
}

// List lists a guest directory.
func (c *Client) List(ctx context.Context, path string) ([]DirEntry, error) {
	cn, done, err := c.open(ctx, Request{Op: OpList, Path: path})
	if err != nil {
		return nil, err
	}
	defer done()
	resp, err := cn.response()
	return resp.Entries, err
}

// DialPort connects to a port on the guest's loopback. The returned
// connection carries that TCP connection's bytes.
func (c *Client) DialPort(ctx context.Context, port int) (io.ReadWriteCloser, error) {
	cn, done, err := c.open(ctx, Request{Op: OpDial, Port: port})
	if err != nil {
		return nil, err
	}
	if _, err := cn.response(); err != nil {
		done()
		return nil, err
	}
	return &dialed{cn: cn, done: done}, nil
}

type dialed struct {
	cn   *conn
	done func()
	once sync.Once
}

func (d *dialed) Read(p []byte) (int, error)  { return d.cn.br.Read(p) }
func (d *dialed) Write(p []byte) (int, error) { return d.cn.rwc.Write(p) }
func (d *dialed) Close() error                { d.once.Do(d.done); return nil }

// CloseWrite passes a half-close on to the guest, when the transport can.
func (d *dialed) CloseWrite() error {
	if cw, ok := d.cn.rwc.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

// Process is a command running in the guest.
type Process struct {
	cn      *conn
	done    func()
	dropped func(int64)   // FrameDropped, on an attach
	wmu     sync.Mutex    // serializes frames to the guest
	exited  chan struct{} // closed once code is final
	code    int
	stdinW  *stdinWriter
}

// Exec starts a command. Output is copied to stdout and stderr as it arrives;
// Wait returns the exit code once all of it has been delivered. ctx bounds the
// whole process, not only the start: when it ends, the connection closes and the
// guest kills the process.
func (c *Client) Exec(ctx context.Context, argv []string, env map[string]string, cwd string, stdout, stderr io.Writer) (*Process, error) {
	return c.ExecRequest(ctx, Request{Op: OpExec, Argv: argv, Env: env, Cwd: cwd}, stdout, stderr)
}

// ExecRequest is Exec with the whole request, for a tty.
func (c *Client) ExecRequest(ctx context.Context, req Request, stdout, stderr io.Writer) (*Process, error) {
	req.Op = OpExec
	cn, done, err := c.open(ctx, req)
	if err != nil {
		return nil, err
	}
	if _, err := cn.response(); err != nil {
		done()
		return nil, err
	}
	p := &Process{cn: cn, done: done, exited: make(chan struct{})}
	p.stdinW = &stdinWriter{p: p}
	go p.pump(stdout, stderr)
	return p, nil
}

// Attach rejoins a kept process (Request.Keep) by its session id: its output
// from offset on — what the guest still holds of it — then live, and its exit
// code. dropped, if not nil, is told how many bytes before what is replayed
// the guest no longer had. The guest answers not_found for a session it does
// not hold.
func (c *Client) Attach(ctx context.Context, session string, offset int64, stdout, stderr io.Writer, dropped func(int64)) (*Process, error) {
	cn, done, err := c.open(ctx, Request{Op: OpAttach, Session: session, Offset: offset})
	if err != nil {
		return nil, err
	}
	if _, err := cn.response(); err != nil {
		done()
		return nil, err
	}
	p := &Process{cn: cn, done: done, exited: make(chan struct{}), dropped: dropped}
	p.stdinW = &stdinWriter{p: p}
	go p.pump(stdout, stderr)
	return p, nil
}

// Sessions lists the kept processes the guest holds.
func (c *Client) Sessions(ctx context.Context) ([]SessionInfo, error) {
	cn, done, err := c.open(ctx, Request{Op: OpSessions})
	if err != nil {
		return nil, err
	}
	defer done()
	resp, err := cn.response()
	return resp.Sessions, err
}

func (p *Process) pump(stdout, stderr io.Writer) {
	code := -1
	defer func() {
		p.done()
		p.code = code
		close(p.exited)
	}()
	for {
		typ, payload, err := ReadFrame(p.cn.br)
		if err != nil {
			return // connection lost: the process is gone as far as we can know
		}
		switch typ {
		case FrameStdout:
			_, _ = stdout.Write(payload)
		case FrameStderr:
			_, _ = stderr.Write(payload)
		case FrameExit:
			if len(payload) == 4 {
				code = int(int32(binary.BigEndian.Uint32(payload)))
			}
			return
		case FrameDropped:
			if p.dropped != nil && len(payload) == 8 {
				// The guest's count, untrusted: reported, never allocated for.
				p.dropped(int64(binary.BigEndian.Uint64(payload) & (1<<62 - 1)))
			}
		default:
			return // a protocol violation ends the conversation
		}
	}
}

func (p *Process) send(typ byte, payload []byte) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	return WriteFrame(p.cn.rwc, typ, payload)
}

// Resize changes a tty process's terminal size.
func (p *Process) Resize(rows, cols uint16) error {
	var b [4]byte
	binary.BigEndian.PutUint16(b[0:2], rows)
	binary.BigEndian.PutUint16(b[2:4], cols)
	return p.send(FrameResize, b[:])
}

// Stdin is the process's standard input.
func (p *Process) Stdin() io.WriteCloser { return p.stdinW }

// Signal delivers a signal by name (INT, TERM, KILL, HUP).
func (p *Process) Signal(sig string) error { return p.send(FrameSignal, []byte(sig)) }

// Wait returns the exit code: the process's own, 128+n for death by signal n,
// or -1 if the connection to the guest was lost first. It may be called more
// than once.
func (p *Process) Wait() int {
	<-p.exited
	return p.code
}

type stdinWriter struct {
	p      *Process
	mu     sync.Mutex
	closed bool
}

func (w *stdinWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, errors.New("stdin is closed")
	}
	n := 0
	for len(b) > 0 {
		chunk := b
		if len(chunk) > MaxFrame {
			chunk = chunk[:MaxFrame]
		}
		if err := w.p.send(FrameStdin, chunk); err != nil {
			return n, err
		}
		n += len(chunk)
		b = b[len(chunk):]
	}
	return n, nil
}

func (w *stdinWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return w.p.send(FrameStdinEOF, nil)
}
