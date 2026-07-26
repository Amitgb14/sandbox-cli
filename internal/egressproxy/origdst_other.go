//go:build !linux

package egressproxy

import (
	"errors"
	"net"
)

// OriginalDestination is Linux-only: SO_ORIGINAL_DST is what iptables REDIRECT
// leaves behind, and neither exists elsewhere.
//
// The stub is here so the package builds and its logic stays testable on a
// developer's macOS machine — the proxy itself only ever runs inside the Linux
// container. Callers fall back to inferring the port from the protocol shape,
// which is what the firewall's redirect rules constrain anyway.
func OriginalDestination(net.Conn) (net.IP, int, error) {
	return nil, 0, errors.New("original destination is only available on linux")
}
