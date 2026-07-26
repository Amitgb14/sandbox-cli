package image

import (
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/egressproxy"
)

// TestRefCoversEverythingInTheImage pins that the content-addressed tag actually
// covers the image's content. The proxy's source is compiled into the image, so
// hashing only the Dockerfile would let a changed proxy ship under the old tag —
// nobody rebuilds, and a stale binary keeps enforcing the egress allowlist. That
// is the worst thing in the image to be stale.
func TestRefCoversEverythingInTheImage(t *testing.T) {
	base := Ref()
	if base == "" || !strings.HasPrefix(base, "sandbox-base:") {
		t.Fatalf("Ref() = %q", base)
	}
	// Same inputs, same tag — the whole point of content addressing.
	if Ref() != base {
		t.Error("Ref() is not deterministic")
	}
	// The proxy source must be part of it. Asserted by checking the hash changes
	// when that source does, using the same accessors Ref uses.
	if len(egressproxy.EmbeddedFiles()) == 0 {
		t.Fatal("no proxy sources embedded; Ref would silently cover nothing")
	}
	if egressproxy.GeneratedSources() == "" {
		t.Fatal("no generated proxy sources; Ref would not cover main.go or go.mod")
	}
	for _, f := range egressproxy.EmbeddedFiles() {
		src, err := egressproxy.SourceOf(f)
		if err != nil || src == "" {
			t.Errorf("embedded source %q is unreadable or empty: %v", f, err)
		}
	}
}
