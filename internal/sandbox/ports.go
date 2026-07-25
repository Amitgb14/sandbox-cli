package sandbox

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// defaultPublishHost is where a port spec with no address of its own is bound.
//
// This is the one place sandbox-cli deliberately differs from `docker -p`.
// Docker binds a bare "3000:3000" to 0.0.0.0 — every interface, so a dev server
// started inside the sandbox is reachable by anything on the same network. For a
// tool whose entire premise is that reach is opt-in and narrow, that is the
// wrong default: you asked to see the port from *your machine*, not to serve it
// to the coffee shop. Writing an address explicitly (0.0.0.0:3000:3000) still
// does exactly what it says.
const defaultPublishHost = "127.0.0.1"

// NormalizePublish validates port specs and returns them fully qualified as
// IP:HOSTPORT:CONTAINERPORT[/proto], so that by the time a spec reaches
// runtime.BuildArgs there is nothing left to infer about what it exposes.
//
// Accepted forms, matching docker's own syntax:
//
//	3000                      -> 127.0.0.1:3000:3000
//	8080:3000                 -> 127.0.0.1:8080:3000
//	127.0.0.1:8080:3000       -> unchanged
//	0.0.0.0:3000:3000         -> unchanged (explicit "expose me to the network")
//	[::1]:8080:3000           -> unchanged
//	3000:3000/udp             -> 127.0.0.1:3000:3000/udp
//	8000-8010:8000-8010       -> 127.0.0.1:8000-8010:8000-8010
//
// An empty input returns nil, not an empty slice, so "nothing published" stays
// distinguishable from "published nothing".
func NormalizePublish(specs []string) ([]string, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(specs))
	seen := map[string]bool{}
	for _, raw := range specs {
		n, err := normalizeOne(raw)
		if err != nil {
			return nil, err
		}
		if seen[n] {
			continue // the same port asked for twice is not an error, just once
		}
		seen[n] = true
		out = append(out, n)
	}
	return out, nil
}

func normalizeOne(raw string) (string, error) {
	spec := strings.TrimSpace(raw)
	if spec == "" {
		return "", fmt.Errorf("invalid --publish %q: empty port spec", raw)
	}

	// Split off an optional /tcp or /udp suffix before anything else, so the
	// protocol can never be mistaken for part of a port.
	proto := ""
	if i := strings.LastIndexByte(spec, '/'); i >= 0 {
		proto = strings.ToLower(spec[i+1:])
		spec = spec[:i]
		switch proto {
		case "tcp", "udp", "sctp":
		default:
			return "", fmt.Errorf("invalid --publish %q: protocol must be tcp, udp or sctp, got %q", raw, proto)
		}
	}

	// An IPv6 address is bracketed, and its colons must not be mistaken for the
	// spec's own separators — peel it off before splitting.
	host := ""
	if strings.HasPrefix(spec, "[") {
		end := strings.IndexByte(spec, ']')
		if end < 0 {
			return "", fmt.Errorf("invalid --publish %q: unterminated IPv6 address (missing \"]\")", raw)
		}
		host = spec[:end+1]
		rest := spec[end+1:]
		if !strings.HasPrefix(rest, ":") {
			return "", fmt.Errorf("invalid --publish %q: expected \":\" after the IPv6 address", raw)
		}
		if ip := net.ParseIP(spec[1:end]); ip == nil {
			return "", fmt.Errorf("invalid --publish %q: %q is not a valid IPv6 address", raw, spec[1:end])
		}
		spec = rest[1:]
	}

	parts := strings.Split(spec, ":")
	var hostPort, containerPort string
	switch {
	case host != "":
		// [::1]:HOST:CONTAINER, or [::1]:PORT
		switch len(parts) {
		case 1:
			hostPort, containerPort = parts[0], parts[0]
		case 2:
			hostPort, containerPort = parts[0], parts[1]
		default:
			return "", fmt.Errorf("invalid --publish %q: want [IPv6]:HOST_PORT:CONTAINER_PORT", raw)
		}
	case len(parts) == 1:
		hostPort, containerPort = parts[0], parts[0]
	case len(parts) == 2:
		hostPort, containerPort = parts[0], parts[1]
	case len(parts) == 3:
		if ip := net.ParseIP(parts[0]); ip == nil {
			return "", fmt.Errorf("invalid --publish %q: %q is not a valid IP address", raw, parts[0])
		}
		host, hostPort, containerPort = parts[0], parts[1], parts[2]
	default:
		return "", fmt.Errorf("invalid --publish %q: want PORT, HOST_PORT:CONTAINER_PORT, or IP:HOST_PORT:CONTAINER_PORT", raw)
	}

	hostSpan, err := parsePortRange(hostPort, raw)
	if err != nil {
		return "", err
	}
	containerSpan, err := parsePortRange(containerPort, raw)
	if err != nil {
		return "", err
	}
	if hostSpan != containerSpan {
		return "", fmt.Errorf("invalid --publish %q: host range covers %d ports but container range covers %d", raw, hostSpan, containerSpan)
	}

	if host == "" {
		host = defaultPublishHost
	}
	out := host + ":" + hostPort + ":" + containerPort
	if proto != "" {
		out += "/" + proto
	}
	return out, nil
}

// parsePortRange validates "N" or "N-M" and returns how many ports it covers, so
// the caller can check that the two sides of a mapping line up.
func parsePortRange(s, raw string) (int, error) {
	lo, hi, isRange := strings.Cut(s, "-")
	loN, err := parsePort(lo, raw)
	if err != nil {
		return 0, err
	}
	if !isRange {
		return 1, nil
	}
	hiN, err := parsePort(hi, raw)
	if err != nil {
		return 0, err
	}
	if hiN < loN {
		return 0, fmt.Errorf("invalid --publish %q: port range %q ends before it starts", raw, s)
	}
	return hiN - loN + 1, nil
}

func parsePort(s, raw string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("invalid --publish %q: missing port number", raw)
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid --publish %q: %q is not a port number", raw, s)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("invalid --publish %q: port %d is out of range (1-65535)", raw, n)
	}
	return n, nil
}
