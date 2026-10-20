package main

import (
	"strings"
	"testing"
)

func TestCheckGPUFlags(t *testing.T) {
	const kit, ch = "/var/lib/sandboxd/gpu-kit", "/usr/local/bin/cloud-hypervisor"
	gpu := []string{"0000:01:00.0"}
	for _, c := range []struct {
		name, backend string
		gpus          []string
		ch, kit       string
		want          string // "" is accepted
	}{
		{"none", "firecracker", nil, "", "", ""},
		{"all three", "firecracker", gpu, ch, kit, ""},
		{"two GPUs", "firecracker", []string{"0000:01:00.0", "0000:41:00.0"}, ch, kit, ""},
		{"no kit", "firecracker", gpu, ch, "", "together"},
		{"no VMM", "firecracker", gpu, "", kit, "together"},
		{"no GPU", "firecracker", nil, ch, kit, "together"},
		{"short address", "firecracker", []string{"01:00.0"}, ch, kit, "PCI address"},
		{"a path", "firecracker", []string{"0000:01:00.0/../.."}, ch, kit, "PCI address"},
		{"twice", "firecracker", []string{"0000:01:00.0", "0000:01:00.0"}, ch, kit, "twice"},
		{"fake", "fake", gpu, "", "", ""},
		{"fake with a kit", "fake", gpu, "", kit, "fake backend takes --gpu alone"},
		{"macos", "macos", gpu, "", "", "for --backend firecracker"},
	} {
		err := checkGPUFlags(c.backend, c.gpus, c.ch, c.kit)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
}
