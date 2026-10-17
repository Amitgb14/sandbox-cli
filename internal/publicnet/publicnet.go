// Package publicnet keeps a connection the host makes on a client's behalf —
// to a URL the client named — on the public internet.
//
// A job's notify URL (the gateway) and a sandbox's metrics endpoint
// (sandboxd) are the client's to choose, and the host connects to them from
// wherever it runs: beside its own loopback services, the nodes' network and
// a cloud's metadata address. So the address is checked as it is dialled,
// after the name is resolved, not when the URL is accepted: a name that
// resolves to a public address when checked and to 127.0.0.1 when used
// (DNS rebinding) is refused at the second. A transport using Control must
// not use a proxy, which would dial on its behalf and be checked instead.
package publicnet

import (
	"fmt"
	"net"
	"net/netip"
	"syscall"
)

// cgnat is the shared address space (RFC 6598), private in all but name.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// Addr reports whether ip is an address on the public internet: not
// loopback, private, link-local (169.254.169.254 among them), shared,
// unspecified or multicast.
func Addr(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !cgnat.Contains(ip)
}

// Control is a net.Dialer's Control that refuses any address Addr does not
// call public. what names the connection in the error ("notify",
// "metrics export"); allow is the operator's flag that lifts the refusal,
// named in the error so it says how.
func Control(what, allow string) func(network, address string, c syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if ip, err := netip.ParseAddr(host); err != nil || !Addr(ip) {
			return fmt.Errorf("%s: %s is not a public address (%s allows it)", what, host, allow)
		}
		return nil
	}
}
