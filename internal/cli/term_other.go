//go:build !linux && !darwin

package cli

import (
	"errors"
	"os"
)

// Without terminal control, runs are streamed rather than interactive.

func isTerminal(*os.File) bool           { return false }
func termSize(*os.File) (uint16, uint16) { return 24, 80 }
func makeRaw(*os.File) (func(), error)   { return nil, errors.New("no raw terminal here") }
func onResize(func()) (stop func())      { return func() {} }
