package main

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// guestInit is PID 1 inside the measurement VM. It prints one line per result
// to the console, prefixed "M3 ", which the host scripts grep for.
func guestInit() error {
	_ = os.MkdirAll("/proc", 0o555)
	_ = os.MkdirAll("/sys", 0o555)
	_ = os.MkdirAll("/dev", 0o755)
	_ = syscall.Mount("proc", "/proc", "proc", 0, "")
	_ = syscall.Mount("sysfs", "/sys", "sysfs", 0, "")
	_ = syscall.Mount("devtmpfs", "/dev", "devtmpfs", 0, "")
	// The root filesystem is built without device nodes, so building it needs no
	// root, and the kernel had no /dev/console to hand init. Reopen it now that
	// devtmpfs exists, or every line below would go nowhere.
	if c, err := os.OpenFile("/dev/console", os.O_RDWR, 0); err == nil {
		for fd := 0; fd <= 2; fd++ {
			_ = syscall.Dup3(int(c.Fd()), fd, 0)
		}
	}

	mode := cmdlineValue("m3.mode")
	fmt.Printf("M3 READY uptime_ms=%d mode=%s\n", uptimeMS(), mode)

	switch mode {
	case "boot":
		// Readiness was the measurement.
	case "net":
		netProbes()
	case "vsock":
		if err := serveVsock(5000); err != nil {
			fmt.Printf("M3 VSOCK ERROR %v\n", err)
		}
		for { // keep the VM alive for the host's measurements
			time.Sleep(time.Hour)
		}
	default:
		fmt.Printf("M3 ERROR unknown m3.mode %q\n", mode)
	}
	fmt.Println("M3 DONE")
	syscall.Sync()
	// With reboot=k the VMM ends the VM on a guest reboot.
	return syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
}

func cmdlineValue(key string) string {
	b, _ := os.ReadFile("/proc/cmdline")
	for _, f := range strings.Fields(string(b)) {
		if v, ok := strings.CutPrefix(f, key+"="); ok {
			return v
		}
	}
	return ""
}

func uptimeMS() int64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return -1
	}
	secs, err := strconv.ParseFloat(strings.Fields(string(b))[0], 64)
	if err != nil {
		return -1
	}
	return int64(secs * 1000)
}

// --- network probes ------------------------------------------------------------
//
// The host redirects the guest's tcp/80 and tcp/443 to the allowlist proxy and
// drops everything else. 1.1.1.1 is used as an arbitrary destination address:
// under redirection the address does not matter, only the name the client sends,
// which is exactly the property being measured. No DNS is configured in the
// guest — whether DNS should exist at all is one of the questions.

type probe struct {
	name   string
	expect bool // true: should get through
	run    func() error
}

func netProbes() {
	const gw = "172.16.0.1"
	probes := []probe{
		{"tls-sni-allowed-redirected", true, func() error { return tlsHead("github.com", "1.1.1.1:443") }},
		{"tls-sni-denied-redirected", false, func() error { return tlsHead("gist.github.com", "1.1.1.1:443") }},
		{"tls-no-sni-ip-literal", false, func() error { return tlsHeadNoSNI("1.1.1.1:443") }},
		{"http-host-allowed-redirected", true, func() error { return httpHead("example.com", "1.1.1.1:80") }},
		{"http-host-denied-redirected", false, func() error { return httpHead("evil.example.net", "1.1.1.1:80") }},
		{"connect-allowed-explicit", true, func() error { return connectTLS(gw+":3128", "github.com") }},
		{"connect-denied-explicit", false, func() error { return connectTLS(gw+":3128", "gist.github.com") }},
		{"tcp-other-port", false, func() error { return tcpDial("1.1.1.1:53") }},
		{"udp-dns", false, udpDNS},
		{"host-ssh", false, func() error { return tcpDial(gw + ":22") }},
		{"host-other-port", false, func() error { return tcpDial(gw + ":8080") }},
	}
	for _, p := range probes {
		start := time.Now()
		err := p.run()
		got := err == nil
		verdict := "PASS"
		if got != p.expect {
			verdict = "UNEXPECTED"
		}
		detail := "connected"
		if err != nil {
			detail = err.Error()
		}
		fmt.Printf("M3 NET %s expect=%s got=%s %s ms=%d detail=%q\n", p.name,
			okWord(p.expect), okWord(got), verdict, time.Since(start).Milliseconds(), detail)
	}
}

func okWord(b bool) string {
	if b {
		return "reach"
	}
	return "blocked"
}

const probeTimeout = 8 * time.Second

func tlsHead(sni, addr string) error {
	d := &net.Dialer{Timeout: probeTimeout}
	c, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: sni})
	if err != nil {
		return err
	}
	defer c.Close()
	return headOver(c, sni)
}

func tlsHeadNoSNI(addr string) error {
	d := &net.Dialer{Timeout: probeTimeout}
	c, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		return err
	}
	defer c.Close()
	return headOver(c, "1.1.1.1")
}

func httpHead(host, addr string) error {
	c, err := net.DialTimeout("tcp", addr, probeTimeout)
	if err != nil {
		return err
	}
	defer c.Close()
	return headOver(c, host)
}

// headOver sends a HEAD request and requires an HTTP status line back.
func headOver(c net.Conn, host string) error {
	_ = c.SetDeadline(time.Now().Add(probeTimeout))
	if _, err := fmt.Fprintf(c, "HEAD / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host); err != nil {
		return err
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, "HTTP/") {
		return fmt.Errorf("not an HTTP response: %q", line)
	}
	return nil
}

func connectTLS(proxyAddr, host string) error {
	c, err := net.DialTimeout("tcp", proxyAddr, probeTimeout)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(probeTimeout))
	fmt.Fprintf(c, "CONNECT %s:443 HTTP/1.1\r\nHost: %s:443\r\n\r\n", host, host)
	br := bufio.NewReader(c)
	status, err := br.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.Contains(status, " 200") {
		return fmt.Errorf("proxy said %q", strings.TrimSpace(status))
	}
	for { // rest of the proxy's response headers
		l, err := br.ReadString('\n')
		if err != nil {
			return err
		}
		if l == "\r\n" || l == "\n" {
			break
		}
	}
	tc := tls.Client(&bufferedConn{c, br}, &tls.Config{ServerName: host})
	if err := tc.Handshake(); err != nil {
		return err
	}
	return headOver(tc, host)
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

func tcpDial(addr string) error {
	c, err := net.DialTimeout("tcp", addr, 4*time.Second)
	if err != nil {
		return err
	}
	return c.Close()
}

// udpDNS sends one DNS query for example.com and waits briefly for any answer.
func udpDNS() error {
	c, err := net.DialTimeout("udp", "1.1.1.1:53", 2*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	q := []byte{0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0,
		7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1}
	if _, err := c.Write(q); err != nil {
		return err
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 512)
	if _, err := c.Read(buf); err != nil {
		return err
	}
	return nil
}

// --- vsock --------------------------------------------------------------------
//
// Protocol, one request per connection:
//
//	'P'                 -> "PONG\n"
//	'R' + uint64 n      guest reads n bytes, then replies "OK\n"
//	'W' + uint64 n      guest writes n bytes

func serveVsock(port uint32) error {
	l, err := listenVsock(port)
	if err != nil {
		return err
	}
	fmt.Printf("M3 VSOCK LISTENING port=%d\n", port)
	for {
		c, err := l.accept()
		if err != nil {
			return err
		}
		go handleVsock(c)
	}
}

func handleVsock(c io.ReadWriteCloser) {
	defer c.Close()
	op := make([]byte, 1)
	if _, err := io.ReadFull(c, op); err != nil {
		return
	}
	switch op[0] {
	case 'P':
		_, _ = c.Write([]byte("PONG\n"))
	case 'R', 'W':
		var n uint64
		if err := binary.Read(c, binary.BigEndian, &n); err != nil {
			return
		}
		if op[0] == 'R' {
			if _, err := io.CopyN(io.Discard, c, int64(n)); err == nil {
				_, _ = c.Write([]byte("OK\n"))
			}
			return
		}
		buf := make([]byte, 1<<20)
		for n > 0 {
			k := uint64(len(buf))
			if n < k {
				k = n
			}
			if _, err := c.Write(buf[:k]); err != nil {
				return
			}
			n -= k
		}
	}
}
