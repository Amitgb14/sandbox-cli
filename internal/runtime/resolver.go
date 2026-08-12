package runtime

// Which runtimes cannot reach the engine's own DNS server.
//
// On a user-defined network docker does not hand the container real resolvers.
// It writes one fake line — `nameserver 127.0.0.11` — and makes that address
// answer by installing a NAT redirect in the container's network namespace,
// pointing at a resolver the daemon runs. The address is a fiction; the redirect
// is what makes it work.
//
// The redirect lives in the *host* kernel's netfilter. A runtime that implements
// its own network stack in userspace never consults it, so 127.0.0.11 is exactly
// what it looks like — an address with nothing behind it — and every name lookup
// in the container fails. Not the allowlist failing: no resolution at all, in
// every network mode.
//
// Measured 2026-08-11 on Rocky Linux 10.2 with a stock `alpine` image and no
// sandbox-cli involvement, so this is a property of the runtime and the engine
// rather than of anything this tool does. `docs/roadmap/gvisor-ingress-test.md`
// carries the reproduction.
var embeddedResolverUnreachable = map[string]bool{
	"runsc":     true, // gVisor: netstack, a userspace TCP/IP stack
	"runsc-kvm": true,
}

// EmbeddedResolverUnreachable reports whether a container on this runtime will
// be unable to use the engine's embedded DNS server, and so needs real resolver
// addresses supplied to it.
//
// A list of names, for the same reason strongerRuntimes is one: there is nothing
// to ask. The engine reports which runtimes are registered and says nothing about
// how any of them implements networking, so the only way to know is to have
// measured it.
//
// It fails in the direction that costs a broken run rather than a silent
// weakening: an unlisted runtime with the same defect gets no resolvers and
// resolves nothing, which is loud and diagnosable. Listing one that does *not*
// have the defect would be worse — it would swap a working embedded resolver for
// the host's, quietly, on a runtime that never needed it.
//
// Kata is deliberately absent. It boots a real kernel, so the question has a
// different answer and has not been measured; adding it on the assumption that
// "a VM is also isolated" is the kind of guess this file exists to avoid.
//
// # Known limitation
//
// It answers about the runtime a run *asked for*, which is "" when the caller
// named none — so a daemon configured with `"default-runtime": "runsc"` in
// daemon.json gets no resolvers, and its containers resolve nothing, while the
// identical container launched with an explicit `--runtime runsc` works.
//
// Not fixed here because the fix is not local: BuildSpec resolves a spec without
// talking to an engine, so answering it means adding a DefaultRuntime call to
// the Runtime interface and paying a `docker info` on every launch to serve an
// unusual configuration. Worth doing if anyone hits it; recorded rather than
// guessed at until then.
func EmbeddedResolverUnreachable(name string) bool {
	return embeddedResolverUnreachable[runtimeName(name)]
}
