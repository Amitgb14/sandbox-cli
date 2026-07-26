package egressproxy

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

// The proxy runs inside the container, so its source has to reach the image
// build. It is embedded here, in the package that owns it, rather than copied
// into the image assets — a copy is a thing that drifts, and the copy would be
// the one that ships.
//
// Building it inside a docker builder stage (rather than cross-compiling on the
// host) is deliberate: sandbox-cli is distributed as a binary, so most users have
// no Go toolchain. Requiring one to build the image would be a new prerequisite
// for everybody in order to serve a feature.
//
// The file list is explicit so test files never ship. TestEmbeddedSourcesAreComplete
// fails if a source file is added to this package and not listed here — otherwise
// a new file would simply be missing from the image and the proxy would fail to
// build long after the change that caused it.
//
//go:embed host.go sni.go server.go origdst_linux.go origdst_other.go
var sources embed.FS

// mainSource is the whole of the proxy's main package.
//
// It is a string rather than an embedded file because a real `package main` in a
// subdirectory of this package would be compiled into the parent module, and a
// `main.go.txt` would be a file extension chosen to lie to the toolchain. All the
// logic worth testing is in the package it imports; this is the wiring.
const mainSource = `// Command sandbox-egress-proxy enforces the egress allowlist by hostname.
// Generated into the image build context by internal/egressproxy.
package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"sandbox-egress-proxy/egressproxy"
)

func main() {
	addr := os.Getenv("SANDBOX_PROXY_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:3128"
	}
	allow := strings.Split(os.Getenv("SANDBOX_EGRESS_ALLOW"), ",")

	m := egressproxy.NewMatcher(allow)
	if m.Len() == 0 {
		// Refuse rather than run permitting nothing by accident, for the same
		// reason BuildSpec refuses an empty allowlist: the strictest-looking
		// configuration must not be the one that silently does nothing.
		log.Fatal("sandbox-egress-proxy: no allowlist supplied; refusing to start")
	}

	l, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("sandbox-egress-proxy: listen %s: %v", addr, err)
	}
	fmt.Fprintf(os.Stderr, "sandbox-cli: egress proxy on %s enforcing %d name(s)\n", addr, m.Len())

	srv := egressproxy.New(m, func(d egressproxy.Decision) {
		// Denials are the half that had no trace before. Logged to stderr, which
		// docker captures, so ` + "`docker logs`" + ` answers "what was refused".
		if !d.Allowed {
			fmt.Fprintf(os.Stderr, "sandbox-cli: egress %s\n", d)
		}
	})
	log.Fatal(srv.Serve(l))
}
`

// goMod is the mini-module the builder stage compiles.
const goMod = "module sandbox-egress-proxy\n\ngo 1.25\n"

// WriteBuildContext lays the proxy source out under dir, ready for a docker
// builder stage to compile.
func WriteBuildContext(dir string, write func(name string, data []byte) error) error {
	if err := write("proxy/go.mod", []byte(goMod)); err != nil {
		return err
	}
	if err := write("proxy/main.go", []byte(mainSource)); err != nil {
		return err
	}
	entries, err := fs.ReadDir(sources, ".")
	if err != nil {
		return err
	}
	for _, e := range entries {
		b, err := sources.ReadFile(e.Name())
		if err != nil {
			return err
		}
		if err := write("proxy/egressproxy/"+e.Name(), b); err != nil {
			return fmt.Errorf("writing %s: %w", e.Name(), err)
		}
	}
	return nil
}

// EmbeddedFiles lists what will ship, for the completeness test.
func EmbeddedFiles() []string {
	entries, err := fs.ReadDir(sources, ".")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// GeneratedSources returns the parts of the build context that are generated
// rather than embedded, so image.Ref can hash them too — a change to the proxy's
// main or its go.mod must produce a new image tag like any other change.
func GeneratedSources() string { return mainSource + goMod }

// SourceOf returns one embedded file, for tests.
func SourceOf(name string) (string, error) {
	b, err := sources.ReadFile(name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
