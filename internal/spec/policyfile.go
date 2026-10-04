package spec

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// policyFile is the operator's policy, as written in /etc/sandboxd/policy.yaml.
// Every field is optional; what is absent keeps DefaultPolicy's value.
//
//	default_image: ghcr.io/you/sandbox-base:1
//	images: [ghcr.io/you/sandbox-base:1]      # only these may be requested
//	defaults: {cpus: 2, memory_mb: 2048, disk_mb: 20480, idle_timeout_secs: 3600}
//	limits:   {max_cpus: 8, max_memory_mb: 16384, max_disk_mb: 102400, max_idle_timeout_secs: 86400}
//	network:
//	  default: {mode: allowlist, allow: [github.com, registry.npmjs.org]}
//	  ceiling: allowlist
//	  may_allow: ["*.internal.example.com"]
//	pools:                                     # sandboxes booted ahead of time
//	  - {image: ghcr.io/you/sandbox-base:1, size: 2}
type policyFile struct {
	DefaultImage *string  `yaml:"default_image"`
	Images       []string `yaml:"images"`
	Defaults     *struct {
		CPUs            *float64 `yaml:"cpus"`
		MemoryMB        *int     `yaml:"memory_mb"`
		DiskMB          *int     `yaml:"disk_mb"`
		IdleTimeoutSecs *int     `yaml:"idle_timeout_secs"`
	} `yaml:"defaults"`
	Limits *struct {
		MaxCPUs            *float64 `yaml:"max_cpus"`
		MaxMemoryMB        *int     `yaml:"max_memory_mb"`
		MaxDiskMB          *int     `yaml:"max_disk_mb"`
		MaxIdleTimeoutSecs *int     `yaml:"max_idle_timeout_secs"`
	} `yaml:"limits"`
	Network *struct {
		Default *struct {
			Mode  string   `yaml:"mode"`
			Allow []string `yaml:"allow"`
			Deny  []string `yaml:"deny"`
		} `yaml:"default"`
		Ceiling  *string  `yaml:"ceiling"`
		MayAllow []string `yaml:"may_allow"`
	} `yaml:"network"`
	Pools []struct {
		Image string `yaml:"image"`
		Size  int    `yaml:"size"`
	} `yaml:"pools"`
}

// LoadPolicy reads an operator policy file over DefaultPolicy and validates the
// result. Unknown keys are an error: a misspelt `celing: none` must not leave the
// server more open than its operator believes.
func LoadPolicy(path string) (Policy, error) {
	p := DefaultPolicy()
	data, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	var f policyFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return p, fmt.Errorf("%s: %w", path, err)
	}
	if f.DefaultImage != nil {
		p.DefaultImage = *f.DefaultImage
	}
	if f.Images != nil {
		p.Images = f.Images
	}
	if d := f.Defaults; d != nil {
		set(&p.DefaultCPUs, d.CPUs)
		set(&p.DefaultMemoryMB, d.MemoryMB)
		set(&p.DefaultDiskMB, d.DiskMB)
		set(&p.DefaultIdleSecs, d.IdleTimeoutSecs)
	}
	if l := f.Limits; l != nil {
		set(&p.Limits.MaxCPUs, l.MaxCPUs)
		set(&p.Limits.MaxMemoryMB, l.MaxMemoryMB)
		set(&p.Limits.MaxDiskMB, l.MaxDiskMB)
		set(&p.Limits.MaxIdleTimeoutSecs, l.MaxIdleTimeoutSecs)
	}
	if n := f.Network; n != nil {
		if n.Default != nil {
			p.Network.Default = api.NetworkPolicy{Mode: n.Default.Mode, Allow: n.Default.Allow, Deny: n.Default.Deny}
		}
		if n.Ceiling != nil {
			p.Network.Ceiling = *n.Ceiling
		}
		if n.MayAllow != nil {
			p.Network.MayAllow = n.MayAllow
		}
	}
	for _, pl := range f.Pools {
		p.Pools = append(p.Pools, Pool{Image: pl.Image, Size: pl.Size})
	}
	// The default itself goes through the same normalisation a request does.
	norm, err := resolveNetwork(&p.Network.Default, NetworkPolicy{Default: p.Network.Default, Ceiling: p.Network.Ceiling, MayAllow: []string{"*"}})
	if err != nil {
		return p, fmt.Errorf("%s: network.default: %w", path, err)
	}
	p.Network.Default = norm
	if err := p.Validate(); err != nil {
		return p, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

func set[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}
