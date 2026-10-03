//go:build !unix

package cli

import "fmt"

// stopServe has no portable form off unix: there is no SIGTERM to send.
//
// Refused rather than approximated. The honest alternative would be killing the
// process outright, which skips the daemon's own cleanup and leaves a socket and
// pid file behind for the next `serve status` to puzzle over — so a person
// stopping it by hand is better off than a command that pretends.
func stopServe(pid int) error {
	return fmt.Errorf("stopping a session server is not implemented on this platform; stop pid %d yourself", pid)
}
