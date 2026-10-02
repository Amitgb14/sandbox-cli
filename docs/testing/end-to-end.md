# End-to-end checks

What the unit tests and the fake backend **cannot** prove, and how to prove it on a
real host.

`go test ./...` runs with no VM and no daemon. The fake backend proves that the
API, the policy and the request building behave; it cannot prove that a VM
booted, that a firewall dropped a packet, or that a host file was left alone by a
real guest. Each such claim gets a row here.

The maintainer runs these on real machines. A row is not done until it has a
dated result.

## How to use this file

- **Adding a feature whose truth depends on a real VM, kernel, network or OS?**
  Add a row in the same change: the claim, why a fake cannot prove it, the host it
  needs, the exact command, and what a pass looks like.
- **Ran one?** Fill in *Last result* with the date, the host and the outcome.
  Record a failure as a failure; do not delete the row.
- **Planned rows** name the milestone that makes them runnable. They exist so the
  milestone's author knows what has to be provable before calling it done.

Hosts:
- **mac**: macOS 26+, arm64, the native `container` runtime installed.
- **linux-kvm**: Linux with `/dev/kvm`, with `sandboxd` running as a system
  service.
- **linux-docker**: beta.15 on `main` only.

## Rows

| # | Claim | Why a fake cannot prove it | Host | Command | Pass looks like | Runnable from | Last result |
|---|---|---|---|---|---|---|---|
| 1 | A symlinked `.git/hooks` refuses the run (PR #177, beta.16) | the refusal is unit-tested, but whether the engine would have followed the link is a property of the real engine | linux-docker | `mkdir -p /tmp/proj && cd /tmp/proj && git init -q && rm -rf .git/hooks && ln -s ~/.ssh .git/hooks && sandbox-cli run -- true` | exits non-zero with `refusing to start: … is a symlink` | now (`main`) | — |
| 2 | beta.15's integration suite still passes with the host-escape fixes | needs a docker daemon | linux-docker | `make test-integration` on `fix/host-escapes` | all pass | now (`main`) | — |
| 3 | A sandbox boots and runs a command | no VM in unit tests | linux-kvm | `SANDBOX_TEST_KERNEL=… SANDBOX_TEST_FIRECRACKER=… go test -tags vm -v ./internal/backend/firecracker` | the guest agent answers; processes run as uid 1001; the image disk is unchanged | M4 | pass 2026-10-02 on the dev host (x86_64 EL10, firecracker 1.17.0): agent answering 56 ms after VMM start |
| 4 | Egress allowlist: an allowed name works, an unlisted one fails, and `deny` wins over `allow` | enforcement is nftables + `egressproxy` on the host, against real DNS | linux-kvm | `sudo -E SANDBOX_TEST_NETWORK=1 SANDBOX_TEST_INTERNET=1 SANDBOX_TEST_IMAGE=<base image> SANDBOX_TEST_KERNEL=… SANDBOX_TEST_FIRECRACKER=… go test -tags vm -run TestVMEgress -v ./internal/backend/firecracker` | every probe as expected, including the allowed names connecting | M5 | refusals pass 2026-10-02 in a user+net namespace (no uplink); allowed-and-connecting needs real root |
| 5 | Egress policy updates on a running sandbox with no unfiltered window | an atomic swap is only observable on a real firewall | linux-kvm | part of `TestVMEgress` (update to none) | after the update nothing resolves or connects | M5 | pass 2026-10-02 in a user+net namespace |
| 14 | The jailer runs each VMM as its own uid in a chroot | needs real root | linux-kvm | `sudo -E SANDBOX_TEST_JAILER=$(command -v jailer) SANDBOX_TEST_KERNEL=… SANDBOX_TEST_FIRECRACKER=… SANDBOX_TEST_IMAGE=… go test -tags vm -run TestVMConformance -v ./internal/backend/firecracker` | all pass; `ps` shows each firecracker as a uid ≥ 900000 | M5 | — |
| 6 | Supported agents work through the host's transparent redirect, with no proxy settings of their own | depends on each agent's real TLS stack sending SNI | linux-kvm | *(needs the M4 image with the agents in it)* | claude, codex and gemini each complete a headless turn | M4 | — |
| 13 | M3 measurements on real hosts | timings, the jailer, egress with an uplink, and everything on macOS | linux-kvm, mac | `scripts/m3/README.md` | a results file per host, folded into the plan's M3 section | now | Linux: done 2026-10-02 (x86_64 EL10, firecracker 1.17.0), 10/11 network probes as designed, the 11th explained by firewalld; macOS: not yet run |
| 7 | Bring-back carries commits and nothing else: a hook planted in the guest never reaches the host | the git half is unit-tested; the bundle crossing vsock is not | linux-kvm, mac | *(fixed in M5/M6)* | commits on `refs/sandbox/bring-back/<id>`; no change under the host `.git/hooks` or `.git/config` | M5, M6 | — |
| 8 | A secret's value never appears in a host process listing or the audit log | `ps` and the log are real host artefacts | linux-kvm, mac | *(fixed in M5/M6)* | `ps -ef` and `audit` show the name, never the value | M5, M6 | — |
| 9 | A read-only bind mount is read-only inside the guest | virtio-fs semantics are the runtime's | mac | `scripts/m3/macos/run-all.sh` (ro bind steps), then a sandbox created with `bind: {read_only: true}` | a write inside the guest fails | M6 | — |
| 16 | Suspend, resume and fork under the jailer | the jailer needs real root | linux-kvm | `sudo -E SANDBOX_TEST_JAILER=$(command -v jailer) … go test -tags vm -run TestVMConformance -v ./internal/backend/firecracker` | SuspendKeepsTheSandbox and ASnapshotForksTheSandbox pass | M8 | — |
| 17 | The TypeScript SDK against a real sandboxd | no Node where it was written | any with Node 18+ | `cd sdk/typescript && npm i && npx tsc --noEmit`, then a short script against `sandboxd --backend fake` over http on loopback | typechecks; create, run, files and follow_output work | M8 | — |
| 15 | The macOS backend against the real runtime | the Linux test replaces the runtime with a fake | mac | `sandboxd --backend macos --listen unix:///tmp/s.sock` then `SANDBOX_CONFORMANCE_ENDPOINT=unix:///tmp/s.sock go test ./internal/api/conformance -run TestEndpoint -v` | all pass; skips name the missing capability | M6 | — |
| 10 | Bind-mount ownership: files the agent writes are editable by the host user | virtio-fs uid mapping is the runtime's | mac | *(M3 measurement, then M6)* | the host user can edit and `git commit` what the agent wrote | M6 | — |
| 11 | The conformance suite passes against a real endpoint | the suite against the fake proves only the fake | linux-kvm, mac | `SANDBOX_CONFORMANCE_ENDPOINT=unix://<socket> SANDBOX_CONFORMANCE_TOKEN=<token> go test ./internal/api/conformance -run TestEndpoint -v` (add `SANDBOX_CONFORMANCE_CA=<pem>` for https) | all pass; skipped tests name the missing capability | M5, M6 | linux-kvm: pass 2026-10-02, sandboxd --backend firecracker unprivileged (30 pass, 3 skips: no egress) |
| 12 | A new agent's sessions show, resume and delete in the Sessions view | Studio is a browser UI against a live daemon | mac or linux-kvm | *(Studio returns in M10)* | the session lists, resumes into the right conversation, and deletes | M10 | — |
