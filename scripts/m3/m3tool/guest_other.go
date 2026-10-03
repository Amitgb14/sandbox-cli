//go:build !linux

package main

import "errors"

// guestInit runs only inside the Linux measurement VM.
func guestInit() error {
	return errors.New("guest-init runs only on Linux, as PID 1 of the measurement VM")
}
