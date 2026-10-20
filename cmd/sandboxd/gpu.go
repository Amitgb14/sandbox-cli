package main

import (
	"errors"
	"fmt"

	"github.com/Amitgb14/sandbox-cli/internal/backend"
)

// checkGPUFlags refuses GPU flags that would do nothing or half of
// something (docs/gpu.md). The firecracker backend needs all three: the GPUs,
// the VMM that can pass them through, and the kit their sandboxes boot. The
// fake backend takes --gpu alone, as pretend GPUs, so allocation can be tried
// without a card.
func checkGPUFlags(backendName string, gpus []string, cloudHV, kit string) error {
	if len(gpus) == 0 && cloudHV == "" && kit == "" {
		return nil
	}
	seen := map[string]bool{}
	for _, g := range gpus {
		if !backend.ValidPCIAddr(g) {
			return fmt.Errorf("--gpu %q: a PCI address like 0000:01:00.0, as lspci -D prints it", g)
		}
		if seen[g] {
			return fmt.Errorf("--gpu %s: given twice", g)
		}
		seen[g] = true
	}
	switch backendName {
	case "firecracker":
		if len(gpus) == 0 || cloudHV == "" || kit == "" {
			return errors.New("GPUs need --gpu, --cloud-hypervisor and --gpu-kit together")
		}
	case "fake":
		if cloudHV != "" || kit != "" {
			return errors.New("--cloud-hypervisor and --gpu-kit are for --backend firecracker; the fake backend takes --gpu alone")
		}
	default:
		return errors.New("--gpu, --cloud-hypervisor and --gpu-kit are for --backend firecracker (or --gpu with fake)")
	}
	return nil
}
