package runtime

import (
	"encoding/json"
	"testing"
	"time"
)

// The shape of `docker inspect` output this package relies on. Pinned as a
// literal rather than described, because every field here is a promise made by
// another program: if docker moves one, the failure is a zero value flowing
// silently into a status table or, worse, into land's verdict.
const sampleInspect = `[{
  "Id": "abc123def4567",
  "Name": "/sandbox-app-feature-a",
  "Created": "2026-07-28T09:00:00.123456789Z",
  "State": {
    "Status": "exited",
    "ExitCode": 90,
    "StartedAt": "2026-07-28T09:00:01.5Z",
    "FinishedAt": "2026-07-28T09:04:00Z"
  },
  "Config": {"Labels": {"sandbox.cli": "1", "sandbox.verify": "go test ./..."}}
}]`

func TestDockerInspectShape(t *testing.T) {
	var raw []dockerInspect
	if err := json.Unmarshal([]byte(sampleInspect), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("got %d entries, want 1", len(raw))
	}
	r := raw[0]
	if r.ID != "abc123def4567" || r.Name != "/sandbox-app-feature-a" {
		t.Errorf("id/name not parsed: %+v", r)
	}
	if r.State.Status != "exited" || r.State.ExitCode != 90 {
		t.Errorf("state not parsed: %+v", r.State)
	}
	// The verify verdict travels as an exit code, so this field in particular
	// must survive: land reads it to decide whether work may be merged.
	if r.Config.Labels["sandbox.verify"] != "go test ./..." {
		t.Errorf("labels not parsed as a JSON object: %+v", r.Config.Labels)
	}
	if got := parseDockerTime(r.Created); got.IsZero() {
		t.Error("Created did not parse; every container has one")
	}
}

func TestParseDockerTime(t *testing.T) {
	cases := []struct {
		name, in string
		wantZero bool
	}{
		{"nanoseconds", "2026-07-28T09:00:00.123456789Z", false},
		{"whole seconds", "2026-07-28T09:00:00Z", false},
		// docker writes this for a container that never started or has not
		// finished, and callers check for the zero Time — so it must land there
		// rather than erroring.
		{"docker's zero value", "0001-01-01T00:00:00Z", true},
		{"unparseable", "not a time", true},
		{"empty", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseDockerTime(tc.in).IsZero(); got != tc.wantZero {
				t.Errorf("IsZero() = %v, want %v", got, tc.wantZero)
			}
		})
	}
}

// The sort is load-bearing: callers take the first entry as "the current run".
// A container in `created` state has no StartedAt at all, so ordering on that put
// a just-launched run behind the previous exited one — and a caller would then
// read the old run's exit code and verify label.
func TestSortNewestFirstPutsAJustCreatedContainerFirst(t *testing.T) {
	base := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	infos := []ContainerInfo{
		{
			ID: "old", State: "exited", ExitCode: 0,
			CreatedAt: base, StartedAt: base.Add(time.Second), FinishedAt: base.Add(time.Minute),
		},
		{
			ID: "new", State: "created",
			CreatedAt: base.Add(time.Hour), // started never: StartedAt is zero
		},
	}
	sortNewestFirst(infos)
	if infos[0].ID != "new" {
		t.Errorf("newest first = %q; a created container must not sort behind an older exited one", infos[0].ID)
	}
}

func TestSortNewestFirstOrdersByCreation(t *testing.T) {
	base := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	infos := []ContainerInfo{
		{ID: "middle", CreatedAt: base.Add(time.Hour)},
		{ID: "oldest", CreatedAt: base},
		{ID: "newest", CreatedAt: base.Add(2 * time.Hour)},
	}
	sortNewestFirst(infos)
	for i, want := range []string{"newest", "middle", "oldest"} {
		if infos[i].ID != want {
			t.Errorf("position %d = %q, want %q", i, infos[i].ID, want)
		}
	}
}
