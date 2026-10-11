package macos

import (
	"context"
	"encoding/json"

	"github.com/Amitgb14/sandbox-cli/internal/backend"
)

// Usage is backend.UsageReader: the runtime's own counters for each running
// sandbox this backend started, read in one call. They are the host's
// measure of the VM, not anything the guest reports.
func (b *Backend) Usage(ctx context.Context) (map[string]backend.Usage, error) {
	out, err := b.cli(ctx, "stats", "--no-stream", "--format", "json")
	if err != nil {
		return nil, err
	}
	return parseStats(out, b.started())
}

// started is the ids of the sandboxes this backend started.
func (b *Backend) started() map[string]bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	ids := make(map[string]bool, len(b.specs))
	for id := range b.specs {
		ids[id] = true
	}
	return ids
}

// parseStats reads `container stats --format json`, keeping the containers
// named in mine: another sandboxd's, and anything else the runtime runs, are
// not this one's to report.
func parseStats(out string, mine map[string]bool) (map[string]backend.Usage, error) {
	var list []struct {
		ID               string `json:"id"`
		CPUUsageUsec     int64  `json:"cpuUsageUsec"`
		MemoryUsageBytes int64  `json:"memoryUsageBytes"`
		MemoryLimitBytes int64  `json:"memoryLimitBytes"`
		NetworkRxBytes   int64  `json:"networkRxBytes"`
		NetworkTxBytes   int64  `json:"networkTxBytes"`
		BlockReadBytes   int64  `json:"blockReadBytes"`
		BlockWriteBytes  int64  `json:"blockWriteBytes"`
		NumProcesses     int    `json:"numProcesses"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil, err
	}
	usage := map[string]backend.Usage{}
	for _, c := range list {
		if !mine[c.ID] {
			continue
		}
		usage[c.ID] = backend.Usage{
			CPUUsec: c.CPUUsageUsec, MemoryBytes: c.MemoryUsageBytes, MemoryLimitBytes: c.MemoryLimitBytes,
			NetRxBytes: c.NetworkRxBytes, NetTxBytes: c.NetworkTxBytes,
			DiskReadBytes: c.BlockReadBytes, DiskWriteBytes: c.BlockWriteBytes, Processes: c.NumProcesses,
		}
	}
	return usage, nil
}
