package egressproxy

// The shape of the lines the proxy writes, defined once.
//
// A decision line is `LogLinePrefix + Decision.String()`, and it used to be
// assembled from three separate literals: the prefix in the proxy's `main`
// (embed.go's mainSource), the verb in Decision.String, and a hand-written copy
// of their concatenation in internal/runtime, which counts refusals by matching
// it. Changing either of the first two would have sent that counter silently to
// zero with every test still green — the drift the package doc in
// internal/agents/bootstrap.go was written about, in a place where the symptom is
// an audit field that reads 0 instead of a crash.
//
// All three now derive from the constants here. This file is in embed.go's
// `//go:embed` list because the proxy compiled into the image uses them too, and
// TestEmbeddedSourcesAreComplete requires anything the proxy needs to ship.
// That has a price worth knowing: internal/image hashes the embedded proxy
// sources into the base-image tag, so touching this file changes the tag and
// costs users one rebuild.
const (
	// LogLinePrefix begins every decision line the proxy writes to stderr.
	LogLinePrefix = "sandbox-cli: egress "

	// DenyLinePrefix begins a line reporting a refused connection. This is what
	// internal/runtime matches on, and the reason these constants are exported.
	DenyLinePrefix = LogLinePrefix + denyVerb + " "
)

// denyVerb and allowVerb are the words Decision.String prints. Unexported
// because nothing outside this package should be assembling a decision line by
// hand — having one way to spell it is the point.
const (
	denyVerb  = "DENY"
	allowVerb = "allow"
)
