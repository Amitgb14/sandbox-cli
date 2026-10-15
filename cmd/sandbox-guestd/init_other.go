//go:build !linux

package main

import (
	"errors"
	"os"
	"time"
)

// runInit is the Linux guest's PID 1; there is no other kind of guest.
func runInit() { os.Exit(1) }

// scratchGrower: a guest is Linux, so there is no scratch to grow elsewhere.
func scratchGrower(*os.File, string) func(int64) error { return nil }

// setClock: a guest is Linux.
func setClock(time.Time) error { return errors.New("not a Linux guest") }
