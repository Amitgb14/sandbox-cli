package cli

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// sandbox-cli's own labels win over --label: a user label must not be able
// to disguise which agent ran or which routing attempt this was.
func TestBuildLabels(t *testing.T) {
	d, _ := agents.LookupInteractive("claude")
	got, err := buildLabels([]string{"team=infra", "agent=codex", "route.id=forged"},
		runSpec{agent: &d, labels: map[string]string{"route.id": "real"}})
	if err != nil {
		t.Fatal(err)
	}
	if got["team"] != "infra" || got["agent"] != "claude" || got["route.id"] != "real" {
		t.Errorf("labels %v", got)
	}
	if _, err := buildLabels([]string{"novalue"}, runSpec{}); err == nil {
		t.Error("a label without = was accepted")
	}
	if l, _ := buildLabels(nil, runSpec{}); l != nil {
		t.Errorf("no labels should send none, got %v", l)
	}
}

// A value is cut inside the server's bound without splitting a character,
// which the server would refuse as invalid UTF-8.
func TestTruncateLabelKeepsUTF8(t *testing.T) {
	s := strings.Repeat("é", 200) // 400 bytes
	got := truncateLabel(s)
	if len(got) > 256 || !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
		t.Errorf("%d bytes, valid %v", len(got), utf8.ValidString(got))
	}
	if truncateLabel("short") != "short" {
		t.Error("a short value changed")
	}
}

// Events print text that came through a client — an argv, a path, a label —
// and none of it reaches the terminal as an escape sequence.
func TestPrintEventsIsTerminalSafe(t *testing.T) {
	code := 2
	var out bytes.Buffer
	err := printEvents(&out, api.EventList{Events: []api.Event{
		{Type: api.EventSandboxCreated, Image: "img", EnvNames: []string{"TOKEN"}, Labels: map[string]string{"k": "v"}},
		{Type: api.EventProcessStarted, PID: 1, Argv: []string{"echo", "\x1b]52;c;bad\x07"}},
		{Type: api.EventProcessExited, PID: 1, ExitCode: &code, DurationMS: 1500},
		{Type: api.EventFileWritten, Path: "/tmp/\x1b[2Jx", Bytes: 3},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if strings.ContainsRune(s, 0x1b) || strings.ContainsRune(s, 0x07) {
		t.Errorf("an escape reached the output: %q", s)
	}
	for _, want := range []string{"env TOKEN", "labels k=v", "pid 1 exit 2 after 1.5s", "3 bytes"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

func TestParseVolumeFlag(t *testing.T) {
	for in, want := range map[string]api.VolumeMount{
		"cache:/data":    {Name: "cache", Path: "/data"},
		"cache:/data:ro": {Name: "cache", Path: "/data", ReadOnly: true},
		"cache:/data:rw": {Name: "cache", Path: "/data"},
	} {
		if got, err := parseVolumeFlag(in); err != nil || got != want {
			t.Errorf("%s: %+v %v", in, got, err)
		}
	}
	for _, bad := range []string{"cache", "cache:/data:maybe", "a:b:c:d"} {
		if _, err := parseVolumeFlag(bad); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
}
