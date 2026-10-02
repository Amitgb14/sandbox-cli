package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/egressproxy"
)

// proxy runs the allowlist proxy on the host. Guest tcp/80 and tcp/443 are
// redirected to it by nftables, so it learns the original port from
// SO_ORIGINAL_DST and the name from SNI or the Host header; an explicit CONNECT
// also works for clients that honour HTTPS_PROXY.
func proxy(args []string) error {
	fl := flag.NewFlagSet("proxy", flag.ContinueOnError)
	listen := fl.String("listen", "172.16.0.1:3128", "address to listen on")
	allow := fl.String("allow", "", "comma-separated allowlist")
	if err := fl.Parse(args); err != nil {
		return err
	}
	m := egressproxy.NewMatcher(strings.Split(*allow, ","))
	if m.Len() == 0 {
		return fmt.Errorf("no allowlist; refusing to start")
	}
	l, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "m3tool proxy: %s, %d pattern(s)\n", l.Addr(), m.Len())
	return egressproxy.New(m, func(d egressproxy.Decision) {
		fmt.Fprintf(os.Stderr, "m3tool proxy: %+v\n", d)
	}).Serve(l)
}
