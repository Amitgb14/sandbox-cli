//go:build !linux

package vsock

import (
	"errors"
	"net"
)

// Listener exists only inside a Linux guest.
type Listener struct{ net.Listener }

// Listen is unavailable off Linux; the guest agent runs in a Linux VM.
func Listen(uint32) (*Listener, error) { return nil, errors.New("vsock listening needs Linux") }
