//go:build !linux

package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/guestproto"
)

// runInit is the Linux guest's PID 1; there is no other kind of guest.
func runInit() { os.Exit(1) }

// scratchGrower: a guest is Linux, so there is no scratch to grow elsewhere.
func scratchGrower(*os.File, string) func(int64) error { return nil }

// setClock: a guest is Linux.
func setClock(time.Time) error { return errors.New("not a Linux guest") }

// statsFunc: a guest is Linux, so there is no guest here to report on.
func statsFunc(int, int) func(context.Context) (guestproto.Stats, error) { return nil }
