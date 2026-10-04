package main

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// nodeOptions are the flags that make a sandboxd one node of many behind a
// gateway (docs/self-hosting.md, "As one node behind a gateway").
type nodeOptions struct {
	id       string
	labels   []string
	cpus     float64
	memoryMB int
	diskMB   int
	clientCA string
}

// checkNodeFlags refuses what the node flags cannot mean, before anything is
// started: a node id that would not split out of a sandbox id again, a label
// the API would refuse on a sandbox, a negative capacity, and a client CA with
// no TLS to ask for client certificates on.
func checkNodeFlags(o nodeOptions, haveTLS bool) (map[string]string, error) {
	if o.id != "" && !api.ValidNodeID(o.id) {
		return nil, fmt.Errorf("--node-id %q: lowercase letters, digits and -, starting with a letter or digit, at most 31", o.id)
	}
	labels, err := parseLabels(o.labels)
	if err != nil {
		return nil, err
	}
	if o.cpus < 0 || o.memoryMB < 0 || o.diskMB < 0 {
		return nil, errors.New("--capacity-cpus, --capacity-memory-mb and --capacity-disk-mb cannot be negative")
	}
	// A client CA means "only the gateway may connect". Without TLS there is
	// no handshake to require a certificate in, and serving anyway would drop
	// the control that was asked for.
	if o.clientCA != "" && !haveTLS {
		return nil, errors.New("--client-ca needs --tls-cert and --tls-key: client certificates are checked in the TLS handshake")
	}
	return labels, nil
}

func parseLabels(in []string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, kv := range in {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("--node-label %q: want key=value", kv)
		}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("--node-label %s is given twice", k)
		}
		out[k] = v
	}
	if err := spec.ValidateLabels(out); err != nil {
		return nil, fmt.Errorf("--node-label: %w", err)
	}
	return out, nil
}

// capacity is what the flags say, and what the machine has where they say
// nothing. Totals, not what is free right now: Free at GET /v1/node is
// capacity less what sandboxes are given, and an operator who wants room left
// for the host says so with the flags. Unknown is 0, which a gateway reads as
// a node with no room rather than one with unlimited room.
func capacity(o nodeOptions, stateDir string) api.NodeResources {
	c := api.NodeResources{CPUs: o.cpus, MemoryMB: o.memoryMB, DiskMB: o.diskMB}
	if c.CPUs == 0 {
		c.CPUs = float64(runtime.NumCPU())
	}
	if c.MemoryMB == 0 {
		c.MemoryMB = hostMemMB()
	}
	if c.DiskMB == 0 {
		c.DiskMB = diskTotalMB(stateDir)
	}
	return c
}

// memTotalMB reads MemTotal from a Linux /proc/meminfo; 0 where there is none.
func memTotalMB(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		rest, ok := strings.CutPrefix(sc.Text(), "MemTotal:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) != 2 || fields[1] != "kB" {
			return 0
		}
		kb, err := strconv.Atoi(fields[0])
		if err != nil || kb < 0 {
			return 0
		}
		return kb / 1024
	}
	return 0
}

// serverTLS is the listener's TLS configuration. With a client CA every
// connection must present a certificate that CA signed, or the handshake
// fails before a request is read; the bearer token is still checked on every
// request after it. Two controls, because they fail differently: a leaked
// token is useless off the private network without the gateway's key, and a
// leaked key is useless without the token.
func serverTLS(certFile, keyFile, clientCA string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("tls: %w", err)
	}
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	if clientCA != "" {
		pem, err := os.ReadFile(clientCA)
		if err != nil {
			return nil, fmt.Errorf("--client-ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("--client-ca %s: no PEM certificate in it", clientCA)
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}
