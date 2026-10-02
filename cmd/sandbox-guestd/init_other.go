//go:build !linux

package main

import "os"

// runInit is the Linux guest's PID 1; there is no other kind of guest.
func runInit() { os.Exit(1) }
