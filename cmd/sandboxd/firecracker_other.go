//go:build !linux

package main

import (
	"errors"

	"github.com/Amitgb14/sandbox-cli/internal/backend"
)

func newFirecracker(backendOptions) (backend.Backend, error) {
	return nil, errors.New("the firecracker backend runs on Linux; on macOS use the macos backend")
}
