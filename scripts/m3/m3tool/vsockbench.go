package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// vsockBench measures the host<->guest channel the guest agent will use,
// through the VMM's unix-socket bridge: a host connects to the socket and asks
// for a guest port with "CONNECT <port>\n".
func vsockBench(args []string) error {
	fl := flag.NewFlagSet("vsock-bench", flag.ContinueOnError)
	uds := fl.String("uds", "", "the VM's vsock unix socket")
	port := fl.Int("port", 5000, "guest vsock port")
	mb := fl.Int("mb", 512, "megabytes to send each way")
	pings := fl.Int("pings", 100, "round trips for the latency figure")
	pingOnly := fl.Bool("ping-only", false, "only check the guest answers")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *uds == "" {
		return fmt.Errorf("--uds is required")
	}
	dial := func() (net.Conn, *bufio.Reader, error) {
		c, err := net.Dial("unix", *uds)
		if err != nil {
			return nil, nil, err
		}
		fmt.Fprintf(c, "CONNECT %d\n", *port)
		br := bufio.NewReader(c)
		line, err := br.ReadString('\n')
		if err != nil || !strings.HasPrefix(line, "OK ") {
			c.Close()
			return nil, nil, fmt.Errorf("vsock bridge said %q (%v)", strings.TrimSpace(line), err)
		}
		return c, br, nil
	}

	// Latency: a fresh connection per ping, since that is what a request costs.
	start := time.Now()
	for i := 0; i < *pings; i++ {
		c, br, err := dial()
		if err != nil {
			return err
		}
		c.Write([]byte{'P'})
		if l, err := br.ReadString('\n'); err != nil || l != "PONG\n" {
			c.Close()
			return fmt.Errorf("ping %d: %q %v", i, l, err)
		}
		c.Close()
	}
	fmt.Printf("M3 VSOCK ping_connect_roundtrip_us=%d over=%d\n", time.Since(start).Microseconds()/int64(*pings), *pings)
	if *pingOnly {
		return nil
	}

	n := uint64(*mb) << 20
	// Upload: host -> guest.
	c, br, err := dial()
	if err != nil {
		return err
	}
	hdr := make([]byte, 9)
	hdr[0] = 'R'
	binary.BigEndian.PutUint64(hdr[1:], n)
	start = time.Now()
	c.Write(hdr)
	buf := make([]byte, 1<<20)
	for sent := uint64(0); sent < n; sent += uint64(len(buf)) {
		if _, err := c.Write(buf); err != nil {
			return err
		}
	}
	if l, err := br.ReadString('\n'); err != nil || l != "OK\n" {
		return fmt.Errorf("upload ack: %q %v", l, err)
	}
	up := time.Since(start)
	c.Close()

	// Download: guest -> host.
	c, br, err = dial()
	if err != nil {
		return err
	}
	hdr[0] = 'W'
	start = time.Now()
	c.Write(hdr)
	if _, err := io.CopyN(io.Discard, br, int64(n)); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	down := time.Since(start)
	c.Close()

	mbps := func(d time.Duration) float64 { return float64(*mb) / d.Seconds() }
	fmt.Printf("M3 VSOCK upload_MBps=%.0f download_MBps=%.0f size_MB=%d\n", mbps(up), mbps(down), *mb)
	return nil
}
