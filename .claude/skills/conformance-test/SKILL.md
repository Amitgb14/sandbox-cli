---
name: conformance-test
description: Write or change tests in the API conformance suite — the one suite run against any sandboxd endpoint (fake, local macOS, self-hosted Linux, cloud). Use when adding or changing API behaviour or a backend.
---

# Conformance tests

"The same API in three modes" is only true where this suite says so. It runs
against **any** endpoint: the in-process fake in `go test`, and a real `sandboxd`
on a Mac or a KVM Linux host when the maintainer runs it there.

The suite is built in M2 (`internal/api`). Until then, this is the contract the
suite is written to.

## Rules

1. **Talk to the endpoint through the public client only.** No backend package
   imports, no peeking at host files, no knowledge of which mode is under test.
   If a test needs to know something, the API must be able to say it.
2. **Gate on capabilities, never on mode.** A test that needs memory snapshots
   asks the endpoint's capability list and skips with the reason when it is
   absent. `if mode == "macos"` is a bug: it hard-codes today's backend into the
   contract.
3. **Each test owns its sandboxes** and terminates them in `t.Cleanup`, even on
   failure. Names are unique per run, so two suites can share an endpoint.
4. **Assert behaviour a client can observe:** states, exit codes, stdout, file
   contents, refusal errors. Never timings, except as generous upper bounds
   labelled as such.
5. **Refusals are tested as carefully as successes.** Each security rule exposed
   through the API gets a conformance test that the refusal happens with a
   legible error:
   - a request that loosens policy;
   - a capability the endpoint lacks;
   - a bundle with two refs;
   - a reference to a sandbox that is not ours.
6. **One behaviour per test, named as a sentence:**
   `TestEgressDenyWinsOverAllow`, not `TestEgress2`.

## Running against a real endpoint

The maintainer runs these. Hand over the command with the endpoint and token
taken from the environment (the exact flag spelling is fixed in M2), and say
what a pass proves on that host and what it cannot prove.
