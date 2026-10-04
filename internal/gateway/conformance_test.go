package gateway

import (
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api/conformance"
)

// The conformance suite is what "the same API" means, and a gateway claims
// to speak it: run whole, through a gateway, with a user's key — not an
// admin's, which would see past the ownership rules the suite must work
// within.

func TestConformanceThroughGatewayOneNode(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	conformance.Run(t, tg.user("suite"))
}

// Three nodes: names, volumes and snapshots now have to be found on the
// right node, and a fork goes where its snapshot is.
func TestConformanceThroughGatewayThreeNodes(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...), startNode(t, "n2", allCaps...), startNode(t, "n3", allCaps...))
	conformance.Run(t, tg.user("suite"))
	spread := 0
	for _, n := range tg.nodes {
		if n.requests.Load() > 0 {
			spread++
		}
	}
	if spread < 2 {
		t.Errorf("the suite's sandboxes all went to one node")
	}
}

// A fleet whose nodes cannot filter egress: every network test must still
// hold through the gateway, with the nodes' own refusals relayed.
func TestConformanceThroughGatewayNoEgress(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1"), startNode(t, "n2"))
	conformance.Run(t, tg.user("suite"))
}
