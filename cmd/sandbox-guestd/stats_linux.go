package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/Amitgb14/sandbox-cli/internal/guestproto"
)

// statsFunc is OpStats for the guest's own agent. The driver's tool runs as
// the sandbox user (uid, gid), not as the agent's root: the GPU's device
// nodes are open to all (loadGPU), so it needs nothing more, and an image
// that left /usr/bin writable would otherwise let the sandbox user replace
// the tool and have it run as the guest's root every few seconds.
func statsFunc(uid, gid int) func(context.Context) (guestproto.Stats, error) {
	return func(ctx context.Context) (guestproto.Stats, error) {
		f, err := os.Open("/proc/meminfo")
		if err != nil {
			return guestproto.Stats{}, err
		}
		defer f.Close()
		_, ctlErr := os.Stat("/dev/nvidiactl")
		return guestStats(ctx, f, ctlErr == nil, func(ctx context.Context) ([]byte, error) {
			return runNvidiaSMI(ctx, uid, gid)
		}), nil
	}
}

func runNvidiaSMI(ctx context.Context, uid, gid int) ([]byte, error) {
	cmd := exec.CommandContext(ctx, nvidiaSMI, nvidiaSMIQuery...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	cmd.Dir = "/"
	if os.Getuid() == 0 && uid >= 0 && gid >= 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}}
	}
	var out bytes.Buffer
	cmd.Stdout = &limitWriter{w: &out, n: maxSMIOutput}
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// limitWriter keeps the first n bytes and fails after them, which ends the
// tool rather than buffering whatever it prints.
type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if len(p) > l.n {
		return 0, errors.New("nvidia-smi printed too much")
	}
	l.n -= len(p)
	return l.w.Write(p)
}
