package main

import (
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/studioapi"
)

// The block's order is part of what it says: the notes are read before somebody
// scans, and the password sentence is the last thing on screen, after the link
// it is about.
func TestPairingBlockOrder(t *testing.T) {
	block, err := pairingBlock(studioapi.PairingOptions{
		URL:          "http://192.168.1.20:8787",
		Addr:         "0.0.0.0:8787",
		Name:         "this mac",
		AllowedHosts: []string{"192.168.1.20"},
		Serving:      true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(block, "\n"), "\n")
	link := "sandboxstudio://pair#v=1&url=http%3A%2F%2F192.168.1.20%3A8787&name=this+mac"

	if !strings.HasPrefix(lines[1], "  note: no -token is set") {
		t.Errorf("line 1 = %q, want the no-token note before the code", lines[1])
	}
	if !strings.HasPrefix(lines[2], "█") {
		t.Errorf("line 2 = %q, want the code to start after the notes", lines[2])
	}
	if got := lines[len(lines)-2]; got != link {
		t.Errorf("second-to-last line = %q, want the link %q", got, link)
	}
	if got := lines[len(lines)-1]; !strings.HasPrefix(got, "Treat this like a password") {
		t.Errorf("last line = %q, want the password warning", got)
	}
}

func TestPairingBlockRefusesLoopback(t *testing.T) {
	if _, err := pairingBlock(studioapi.PairingOptions{Addr: "127.0.0.1:8787", Token: "fake"}, false); err == nil {
		t.Fatal("want a refusal: a phone cannot dial this machine's loopback")
	}
}
