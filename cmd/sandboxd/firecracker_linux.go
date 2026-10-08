package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Amitgb14/sandbox-cli/internal/backend"
	fc "github.com/Amitgb14/sandbox-cli/internal/backend/firecracker"
	"github.com/Amitgb14/sandbox-cli/internal/image"
)

func newFirecracker(o backendOptions) (backend.Backend, error) {
	if o.kernel == "" {
		return nil, errors.New("--kernel is required for the firecracker backend")
	}
	bin, err := exec.LookPath(o.firecracker)
	if err != nil {
		return nil, fmt.Errorf("--firecracker %s: %w", o.firecracker, err)
	}
	agent := o.agent
	if agent == "" {
		self, _ := os.Executable()
		agent = filepath.Join(filepath.Dir(self), "sandbox-guestd")
	}
	puller := &image.Puller{Logf: o.logf, PlainHTTP: map[string]bool{}}
	for _, r := range o.insecureRegistries {
		puller.PlainHTTP[r] = true
		o.logf("pulling from %s over plain HTTP", r)
	}
	cfg := fc.Config{
		Firecracker: bin, Kernel: o.kernel, Agent: agent, StateDir: o.stateDir,
		Puller: puller, Logf: o.logf, Keep: o.keep,
	}
	if o.network {
		cfg.Network = &fc.Network{ProxyPort: o.proxyPort, DNSPort: o.dnsPort, Logf: o.logf}
	} else {
		o.logf("networking off: sandboxes have no network interface")
	}
	if o.jailer != "" {
		cfg.Jailer = &fc.Jailer{Path: o.jailer, ChrootBase: filepath.Join(o.stateDir, "jail"), UIDBase: 900000}
	}
	return fc.New(cfg)
}
