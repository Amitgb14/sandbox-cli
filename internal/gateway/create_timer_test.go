package gateway

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The slow-create log line formats the timer as a value, which once
// printed its fields (%!s(uint64=…)) instead of its steps.
func TestCreateTimerPrintsItsSteps(t *testing.T) {
	c := createTimer{start: time.Now()}
	c.mark("checks")
	c.mark("node demo")
	got := fmt.Sprintf("%s", c)
	if !strings.HasPrefix(got, "checks ") || !strings.Contains(got, ", node demo ") || strings.Contains(got, "%!") {
		t.Fatalf("%q, want its steps", got)
	}
}
