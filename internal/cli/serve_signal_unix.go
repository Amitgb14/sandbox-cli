//go:build unix

package cli

import (
	"fmt"
	"syscall"
)

// stopServe asks a session server to exit.
//
// SIGTERM rather than SIGKILL, and the difference is the catalog: the daemon
// removes its socket and pid file on the way out, so a killed one leaves both
// behind and the next `serve status` has to work out that the pid is stale. The
// containers are untouched either way — the engine owns them.
func stopServe(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("signalling pid %d: %w", pid, err)
	}
	return nil
}
