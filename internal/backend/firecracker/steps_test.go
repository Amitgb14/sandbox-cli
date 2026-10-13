//go:build linux

package firecracker

import (
	"fmt"
	"regexp"
	"testing"
	"time"
)

// A create's log line names each step with its own time, not the time
// since the start.
func TestStepsTimeEachStep(t *testing.T) {
	s := steps{start: time.Now().Add(-2 * time.Second)}
	s.mark("image")
	s.mark("boot")
	got := fmt.Sprintf("%s", &s)
	if !regexp.MustCompile(`^image 2(\.\d+)?s, boot \S+$`).MatchString(got) {
		t.Fatalf("%q, want the image's 2s and then the boot's own time", got)
	}
}
