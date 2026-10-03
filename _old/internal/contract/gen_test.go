package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const repoRoot = "../.."

// The checked-in mirror must be what the generator produces from the Go types.
//
// This is the whole point of the generator. Before it, the mirror was
// hand-maintained and had drifted in the way hand-maintained copies do —
// silently, and in the fields that had just changed: `AgentInfo` was three
// fields where the Go struct had ten, and `SessionSummary` was missing entirely,
// so the one shape whose meaning had changed was the one a client could not read
// the contract for. Nothing catches that except a test that regenerates and
// compares.
func TestContractMirrorIsInSync(t *testing.T) {
	want, err := Generate(RootFile(repoRoot), Deps(repoRoot), Preamble)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	// Both copies, because the SDK ships its own and a package that drifted from
	// the documentation would be the same failure one directory over.
	for _, rel := range [][]string{
		{"docs", "studio-api", "types.ts"},
		{"sdk", "typescript", "src", "contract.ts"},
	} {
		checkMirror(t, filepath.Join(append([]string{repoRoot}, rel...)...), want)
	}
}

// The Swift mirror is held to the same standard as the TypeScript one.
//
// Only the copy under docs/ is checked. The iOS app repository gets a second
// write from `make contract IOSAPP=…` and is not checked out here, so testing it
// would make this test pass or fail on whether a sibling directory happens to
// exist — which is the kind of test people learn to ignore. The copy that is
// always present is the one that has to be right, and an app built from a stale
// checkout of *that* is a problem this repository cannot see anyway.
func TestSwiftMirrorIsInSync(t *testing.T) {
	want, err := GenerateSwift(RootFile(repoRoot), Deps(repoRoot), SwiftPreamble)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	checkMirror(t, filepath.Join(repoRoot, "docs", "studio-api", "Contract.swift"), want)
}

// Both mirrors end with their hand-written tail.
//
// This is the one thing the drift tests structurally cannot see. They compare a
// checked-in file to a fresh render, so a generator that forgets to append the
// tail produces two identical files and a green suite — which is exactly how
// `RunListQuery` went missing twice: once when the TypeScript generator replaced
// the hand-maintained mirror, and again when the Swift generator shipped
// appending no tail at all. Both times the query shape of the most-used listing
// endpoint simply stopped being described, and nothing failed.
//
// Asserting the *suffix* rather than a type name keeps it true of whatever the
// tail grows to hold next.
func TestMirrorsCarryTheHandWrittenTail(t *testing.T) {
	ts, err := Generate(RootFile(repoRoot), Deps(repoRoot), Preamble)
	if err != nil {
		t.Fatalf("generating TypeScript: %v", err)
	}
	if !strings.HasSuffix(ts, Extras) {
		t.Error("the TypeScript mirror does not end with extras.ts — the hand-written tail was not appended")
	}

	sw, err := GenerateSwift(RootFile(repoRoot), Deps(repoRoot), SwiftPreamble)
	if err != nil {
		t.Fatalf("generating Swift: %v", err)
	}
	if !strings.HasSuffix(sw, SwiftExtras) {
		t.Error("the Swift mirror does not end with extras.swift — the hand-written tail was not appended")
	}
}

// Every property name in the Swift mirror is either the wire key itself or is
// declared in a CodingKeys block.
//
// Swift's CodingKeys is all-or-nothing: a block that lists some fields silently
// drops every field missing from it, and the symptom is a decoded struct with
// default values rather than an error. So the emitter must either leave every
// name alone or spell every one of them out, and this is what says it did.
func TestSwiftNamesSurviveDecoding(t *testing.T) {
	out, err := GenerateSwift(RootFile(repoRoot), Deps(repoRoot), SwiftPreamble)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	for _, block := range strings.Split(out, "\npublic struct ")[1:] {
		name, _, _ := strings.Cut(block, ":")
		body, _, _ := strings.Cut(block, "\n    public init(")
		hasKeys := strings.Contains(body, "enum CodingKeys")

		var props []string
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if rest, ok := strings.CutPrefix(line, "public var "); ok {
				prop, _, _ := strings.Cut(rest, ":")
				props = append(props, strings.TrimSpace(prop))
			}
		}
		if !hasKeys {
			// No block, so every property must already be spelled like its key.
			// A backtick means the emitter escaped a Swift keyword, and a
			// backticked identifier still encodes under its bare name — so it is
			// the one rewrite that needs no CodingKeys entry.
			continue
		}
		for _, p := range props {
			if !strings.Contains(body, "case "+p+" = ") {
				t.Errorf("%s.%s is renamed but missing from CodingKeys — it would decode as its default", name, p)
			}
		}
	}
}

func checkMirror(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if string(got) == want {
		return
	}

	// The failure names the first line that differs rather than printing two
	// files: a diff of 1600 lines is a wall, and what a reader needs is the
	// field that moved and the one command that fixes it.
	gotLines, wantLines := strings.Split(string(got), "\n"), strings.Split(want, "\n")
	for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
		g, w := "", ""
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			t.Fatalf("%s is out of date with internal/studioapi/types.go\n"+
				"  first difference at line %d\n"+
				"    checked in: %q\n"+
				"    generated:  %q\n"+
				"  run `make contract` to regenerate it",
				path, i+1, g, w)
		}
	}
	t.Fatalf("%s and the generated output differ in length but not in any line", path)
}

// A field the server omits when empty must be optional in the mirror, and a
// field it always sends must not be.
//
// Absent and zero are different answers throughout this API — an unset exit
// code is a run that has not finished, an empty `routedFrom` is a run that used
// the agent it was given — and a mirror that made everything optional would
// erase the distinction the Go side is careful to draw.
func TestOptionalityFollowsOmitempty(t *testing.T) {
	out, err := Generate(RootFile(repoRoot), Deps(repoRoot), Preamble)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	for _, want := range []string{
		"  id: string;",               // Run.ID — always sent
		"  exitCode?: number;",        // set only once a run has exited
		"  handoffFrom?: string;",     // absent unless somebody handed the work over
		"  dockerAvailable: boolean;", // health always answers this
	} {
		if !strings.Contains(out, want) {
			t.Errorf("generated mirror is missing %q", want)
		}
	}
}

// Optional and nullable are different claims, and the emitter must not conflate
// them.
//
// `omitempty` says the key may be absent. A pointer *without* it says the key is
// always sent and may be null, because encoding/json writes nil as null rather
// than omitting it. The first version of this generator marked both `?`, which
// erased null from seventeen fields — including Worktree.verified, whose own Go
// comment says "null when nothing checked it… Null is not false", and which
// `land` refuses on. A client testing `=== undefined` for that would have
// rendered an unverified branch exactly like a failed one.
func TestNullableIsNotOptional(t *testing.T) {
	out, err := Generate(RootFile(repoRoot), Deps(repoRoot), Preamble)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	for _, want := range []string{
		"  verified: boolean | null;",   // *bool, no omitempty
		"  utilization: number | null;", // *float64, no omitempty
		"  exitCode?: number;",          // *int with omitempty: absent, not null
	} {
		if !strings.Contains(out, want) {
			t.Errorf("generated mirror is missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"  verified?: boolean;",
		"  utilization?: number;",
	} {
		if strings.Contains(out, unwanted) {
			t.Errorf("%q renders a null the server always sends as an absent key", unwanted)
		}
	}
}

// The generator must refuse what it cannot render rather than emit a name with
// no declaration behind it.
//
// It used to fail open: an unresolvable type became a dangling reference, an
// unmappable field vanished from the interface, and `make contract` exited 0
// with the drift test green — because that test compares the output to itself.
// A generator whose failures are quieter than its successes is worse than no
// generator.
func TestGenerateRefusesWhatItCannotRender(t *testing.T) {
	if _, err := Generate(filepath.Join("testdata", "dangling.go"), nil, ""); err == nil {
		t.Error("a field referencing an undeclared type generated without error")
	} else if !strings.Contains(err.Error(), "unresolved type") {
		t.Errorf("unhelpful error for a dangling reference: %v", err)
	}
	if _, err := Generate(filepath.Join("testdata", "embedded.go"), nil, ""); err == nil {
		t.Error("an embedded struct generated without error; encoding/json promotes its fields onto the wire")
	}
}

// A named string type with declared values becomes a union, because a client
// switching on it exhaustively should be checked by its own compiler rather
// than by hope.
func TestNamedStringTypesBecomeUnions(t *testing.T) {
	out, err := Generate(RootFile(repoRoot), Deps(repoRoot), Preamble)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	for _, want := range []string{
		"export type RunState =",
		`  "running" |`,
		"export type LogEventType =",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("generated mirror is missing %q", want)
		}
	}
	if strings.Contains(out, "export type RunState = string;") {
		t.Error("RunState rendered as a bare string; its declared values are the contract")
	}
}
