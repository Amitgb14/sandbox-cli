package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/protocol"
)

// Handler answers one op. Split out so the dispatch table is data rather than a
// switch that grows a case per phase.
type Handler func(ctx context.Context, params json.RawMessage) (any, *protocol.Error)

// Serve listens on the session socket until ctx is cancelled.
//
// The socket is unlinked first, which is the one piece of housekeeping a unix
// socket always needs: bind fails on an existing path, and the path always exists
// after a daemon is killed rather than stopped. Unlinking is safe because the
// session directory is 0700 and the path is derived from the repository — there is
// nothing else it could be.
func (s *Server) Serve(ctx context.Context, l Lister) error {
	// Before anything: a path too long to bind fails with "invalid argument",
	// which names nothing. Checked here rather than left to net.Listen so the
	// message says what to shorten.
	if err := CheckSockPath(s.dir); err != nil {
		return err
	}
	sock := SockPath(s.dir)
	if err := os.Remove(sock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clearing the old socket at %s: %w", sock, err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", sock, err)
	}
	defer ln.Close()
	// 0600 explicitly rather than relying on the umask, which a parent process
	// chose and which `SANDBOX_UMASK=0000` would have set to something else. The
	// directory is already 0700; this is the second of the two, and the one that
	// matters if the directory's mode is ever loosened.
	if err := os.Chmod(sock, 0o600); err != nil {
		return fmt.Errorf("securing %s: %w", sock, err)
	}

	if err := os.WriteFile(PIDPath(s.dir), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		return err
	}
	// The pid file is removed on the way out, so `serve status` can tell a daemon
	// that stopped from one that is running. A daemon that is *killed* leaves it
	// behind, which is why status checks the process rather than the file's
	// existence.
	defer os.Remove(PIDPath(s.dir))
	defer os.Remove(sock)

	// Closing the listener is what unblocks Accept; there is no deadline on a unix
	// listener worth setting instead.
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				wg.Wait()
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.handleConn(ctx, conn, l)
		}()
	}
}

// handleConn reads NDJSON requests and writes NDJSON responses until the peer
// goes away.
//
// One request at a time, in order, on one connection. Concurrency is per
// connection rather than per request: the ops are cheap, the catalog is behind a
// mutex anyway, and a client that wants two answers at once opens two
// connections — which it has to do regardless, since an event subscription turns
// a connection one-way.
func (s *Server) handleConn(ctx context.Context, conn net.Conn, l Lister) {
	defer conn.Close()

	r := bufio.NewReaderSize(conn, 64<<10)
	w := bufio.NewWriter(conn)
	enc := json.NewEncoder(w)

	// hello is required first, and the requirement is not ceremony: it is what
	// reports the protocol revision, so a client speaking a different one finds
	// out before it has asked for anything and can say so in its own words.
	greeted := false

	for {
		line, err := readLine(r, protocol.MaxMessage)
		if err != nil {
			return
		}
		if len(line) == 0 {
			continue
		}

		var req protocol.Request
		if err := json.Unmarshal(line, &req); err != nil {
			// No id to answer with, so the error carries an empty one rather than
			// a made-up one. A client matching on id will not match it, which is
			// correct: it did not send a request.
			writeResp(enc, w, protocol.Response{
				OK:    false,
				Error: protocol.Errorf(protocol.CodeInvalid, "not a JSON request object: %v", err),
			})
			continue
		}

		if !greeted && req.Op != protocol.OpHello {
			writeResp(enc, w, protocol.Response{ID: req.ID, OK: false,
				Error: protocol.Errorf(protocol.CodeInvalid,
					"%s must be the first op on a connection: it is what reports the protocol revision, so a client on a different one learns it before asking for anything",
					protocol.OpHello)})
			continue
		}

		result, perr := s.dispatch(ctx, req, l)
		if req.Op == protocol.OpHello && perr == nil {
			greeted = true
		}
		resp := protocol.Response{ID: req.ID, OK: perr == nil, Result: result, Error: perr}
		if err := writeResp(enc, w, resp); err != nil {
			return
		}
	}
}

// dispatch routes one op. Ops not yet implemented answer CodeInvalid by falling
// off the end, which is deliberate: an op that accepts a request and does nothing
// is worse than one that refuses, because a client cannot tell the first from
// success.
func (s *Server) dispatch(ctx context.Context, req protocol.Request, l Lister) (any, *protocol.Error) {
	switch req.Op {
	case protocol.OpHello:
		var p protocol.HelloParams
		if err := decodeParams(req.Params, &p); err != nil {
			return nil, err
		}
		sess := s.Snapshot()
		return protocol.HelloResult{
			Protocol: protocol.Version,
			Engine:   sess.Engine,
			Session:  &sess,
		}, nil

	case protocol.OpSnapshot:
		// Re-read the engine before answering. The catalog is a cache of something
		// that changes without telling us — a container exits, somebody runs
		// `docker kill` — so a snapshot that trusted its own memory would report a
		// dead agent as live, which is the one error that costs somebody an hour.
		if err := s.refresh(ctx, l); err != nil {
			return nil, err
		}
		return s.Snapshot(), nil

	case protocol.OpPaneList:
		var p protocol.PaneListParams
		if err := decodeParams(req.Params, &p); err != nil {
			return nil, err
		}
		if err := s.refresh(ctx, l); err != nil {
			return nil, err
		}
		panes := s.Panes(p)
		if panes == nil {
			// An empty array, never null: a client that checks `.panes.length` on
			// null gets a different kind of error than the one that is true.
			panes = []protocol.Pane{}
		}
		return protocol.PaneListResult{Panes: panes}, nil
	}

	return nil, protocol.Errorf(protocol.CodeInvalid,
		"unknown op %q; this sandbox-cli implements %s, %s and %s",
		req.Op, protocol.OpHello, protocol.OpSnapshot, protocol.OpPaneList)
}

// refresh re-adopts from the engine and persists the result.
//
// A failed save is **not** a failed request. The catalog in memory is already
// correct — it was just rebuilt from the engine — and refusing to answer because
// a file could not be written would make a full disk look like a broken daemon.
// The engine failing is different and does refuse: then the answer itself would
// be a guess.
func (s *Server) refresh(ctx context.Context, l Lister) *protocol.Error {
	if l == nil {
		return protocol.Errorf(protocol.CodeEngineUnavailable, "no container engine is configured for this session")
	}
	if err := s.Adopt(ctx, l); err != nil {
		return protocol.Errorf(protocol.CodeEngineUnavailable, "%v", err)
	}
	if err := s.Save("refresh"); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox-cli: the catalog could not be written (%v); this answer is still from the engine\n", err)
	}
	return nil
}

func decodeParams(raw json.RawMessage, into any) *protocol.Error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return protocol.Errorf(protocol.CodeInvalid, "params: %v", err)
	}
	return nil
}

func writeResp(enc *json.Encoder, w *bufio.Writer, resp protocol.Response) error {
	if err := enc.Encode(resp); err != nil {
		return err
	}
	return w.Flush()
}

// readLine reads one newline-terminated message, refusing one longer than max.
//
// The bound is the point. A line-oriented protocol with unbounded lines is one
// where a client that never sends a newline grows the server's memory until it
// dies, and `bufio.Scanner`'s own answer to that is to return an error the caller
// usually ignores. Here the refusal is explicit and the connection ends, because
// a peer that has already sent a megabyte without a newline is not going to start.
func readLine(r *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > max {
			return nil, fmt.Errorf("message longer than %d bytes", max)
		}
		if err == nil {
			// Trim the newline and any carriage return a client on the other side
			// of a pipe may have added.
			for len(buf) > 0 && (buf[len(buf)-1] == '\n' || buf[len(buf)-1] == '\r') {
				buf = buf[:len(buf)-1]
			}
			return buf, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(buf) > 0 {
			return buf, nil
		}
		return nil, err
	}
}

// Call opens the session socket, says hello, sends one request and returns the
// response. The client half of the protocol, used by every CLI command here.
//
// One connection per call. A persistent client would be faster and is not worth
// the lifecycle: these are one-shot commands, and the cost is a connect to a unix
// socket.
func Call(sock, op string, params any, into any) error {
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	r := bufio.NewReader(conn)
	enc := json.NewEncoder(conn)

	if err := roundTrip(enc, r, protocol.OpHello, protocol.HelloParams{
		Client: "cli", Version: "1",
	}, nil); err != nil {
		return err
	}
	return roundTrip(enc, r, op, params, into)
}

func roundTrip(enc *json.Encoder, r *bufio.Reader, op string, params, into any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := enc.Encode(protocol.Request{ID: "1", Op: op, Params: raw}); err != nil {
		return err
	}
	line, err := readLine(r, protocol.MaxMessage)
	if err != nil {
		return fmt.Errorf("reading the reply to %s: %w", op, err)
	}
	var resp struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  *protocol.Error `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("the reply to %s was not a protocol response: %w", op, err)
	}
	if !resp.OK {
		if resp.Error != nil {
			return resp.Error
		}
		return fmt.Errorf("%s failed with no reason given", op)
	}
	if into != nil && len(resp.Result) > 0 {
		return json.Unmarshal(resp.Result, into)
	}
	return nil
}
