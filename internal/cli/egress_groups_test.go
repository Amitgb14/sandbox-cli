package cli

import (
	"slices"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/policy"
)

// --allow-group sends a gateway's group by name on an allowlist. With no
// other host, the allow list goes as empty, never absent: absent is the
// server's default list, which would stand in for the groups.
func TestAllowGroupAsksForAnAllowlistByName(t *testing.T) {
	caps := api.Capabilities{Network: api.NetworkCeiling{Default: api.NetworkPolicy{Mode: api.NetworkOpen}, Ceiling: api.NetworkOpen}}
	off := false

	n, err := resolveNetwork(policy.Default(), nil, nil, []string{"go"}, caps, "")
	if err != nil || n == nil || n.Mode != api.NetworkAllowlist || !slices.Equal(n.Groups, []string{"go"}) || !slices.Contains(n.Allow, policy.BaselineEgress()[0]) {
		t.Fatalf("a group with the baseline: %+v, %v", n, err)
	}
	bare := policy.Default()
	bare.Network.Baseline = &off
	n, err = resolveNetwork(bare, nil, nil, []string{"go"}, caps, "")
	if err != nil || n.Allow == nil || len(n.Allow) != 0 || !slices.Equal(n.Groups, []string{"go"}) {
		t.Fatalf("a group alone: %+v, %v", n, err)
	}
	bare.Network.Mode = "allowlist"
	if _, err := resolveNetwork(bare, nil, nil, nil, caps, ""); err == nil {
		t.Error("an allowlist of nothing, without a group, was sent")
	}
	for _, mode := range []string{"none", "default"} {
		cfg := policy.Default()
		cfg.Network.Mode = mode
		if _, err := resolveNetwork(cfg, nil, nil, []string{"go"}, caps, ""); err == nil {
			t.Errorf("a group beside network %s was sent", mode)
		}
	}

	n, err = updatedNetwork(api.NetworkAllowlist, nil, nil, []string{"go"}, true, caps, "")
	if err != nil || n.Allow == nil || len(n.Allow) != 0 || !slices.Equal(n.Groups, []string{"go"}) {
		t.Fatalf("update with a group alone: %+v, %v", n, err)
	}
	if _, err := updatedNetwork(api.NetworkAllowlist, nil, nil, nil, true, caps, ""); err == nil {
		t.Error("update to an allowlist of nothing was sent")
	}
}
