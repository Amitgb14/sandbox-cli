package agentusage

import (
	"testing"
	"time"
)

// The distinction this makes is the difference between "nobody has used the
// agent for three weeks" and "the agent runs daily and no longer records usage
// here" — which look identical in the figure alone, and have opposite remedies.
func TestAbandoned(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name    string
		fetched time.Time
		modAt   time.Time
		want    bool
	}{
		{"idle machine: file and reading age together", now.Add(-19 * 24 * time.Hour), now.Add(-19 * 24 * time.Hour), false},
		{"in use and recording: both recent", now.Add(-2 * time.Minute), now.Add(-1 * time.Minute), false},
		{"written today, reading from July", now.Add(-19 * 24 * time.Hour), now, true},
		{"a few hours apart is ordinary", now.Add(-5 * time.Hour), now, false},
		// The claim is "a refresh cannot help", so a day of drift is not enough
		// to make it: the agent rewrites this file for reasons unrelated to usage.
		{"a day apart is not yet a dead source", now.Add(-26 * time.Hour), now, false},
		{"no file time: nothing to compare", now.Add(-19 * 24 * time.Hour), time.Time{}, false},
		{"no reading stamp: already reported unaged", time.Time{}, now, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Snapshot{FetchedAt: tc.fetched, SourceModAt: tc.modAt}
			if got := s.Abandoned(); got != tc.want {
				t.Fatalf("Abandoned() = %v, want %v", got, tc.want)
			}
		})
	}
}
