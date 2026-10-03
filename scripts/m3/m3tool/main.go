// Command m3tool is the helper for the M3 measurements (scripts/m3/README.md).
// It is a measuring instrument, not product code: nothing in cmd/ or internal/
// imports it, and nothing it does is a design decision.
//
// Subcommands:
//
//	guest-init   PID 1 of the measurement VM: report readiness, run the network
//	             probes, or serve vsock — chosen by m3.mode= on the kernel command
//	             line. The whole guest root filesystem is this binary plus a CA
//	             bundle, so every result is printed from inside the VM and owes
//	             nothing to a distribution image.
//	vsock-bench  host side of the vsock throughput test, through the VMM's
//	             unix-socket bridge.
//	proxy        the egress allowlist proxy (internal/egressproxy) on the host,
//	             where the plan now puts enforcement.
package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	// As PID 1 the kernel starts this as /init with no subcommand.
	if os.Getpid() == 1 {
		if err := guestInit(); err != nil {
			fmt.Println("M3 ERROR " + err.Error())
		}
		// PID 1 must not exit: the kernel would panic over the result. A sleep, not
		// an empty select — with nothing else running, the runtime reads select{}
		// as a deadlock and exits anyway.
		for {
			time.Sleep(time.Hour)
		}
	}
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: m3tool guest-init | vsock-bench | proxy [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "guest-init":
		err = guestInit()
	case "vsock-bench":
		err = vsockBench(os.Args[2:])
	case "proxy":
		err = proxy(os.Args[2:])
	default:
		err = fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "m3tool: "+err.Error())
		os.Exit(1)
	}
}
