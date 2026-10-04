# Documentation

sandbox-cli runs isolated microVM sandboxes behind one API: on your Mac, on a
Linux machine you control, or on many machines behind a gateway. `sandboxd`
serves the API on each machine; the CLI, Studio and the SDKs are its clients;
`sandbox-gateway` puts many machines behind one address, with users, SSH, jobs
and services.

These pages are grouped as the website's documentation is, and the site
renders them from here.

## Getting started

| Page | What it answers |
|---|---|
| [Local on a Mac](local-macos.md) | sandboxd with the native `container` runtime, and the points still to be measured |
| [Self-hosting on Linux](self-hosting.md) | sandboxd on a Linux machine: install, TLS and tokens, the network default, pools, volumes, the audit log, how egress is enforced |

## Components

| Page | What it answers |
|---|---|
| [sandbox-cli](cli.md) | Every command and flag, generated from the CLI itself |
| [sandboxd](sandboxd.md) | The server's flags, the policy file, node mode and metrics |
| [Studio](studio.md) | The browser view: each screen, how a gateway key's scopes decide them, organizations, the hosted build, its guards |
| [SDKs](../sdk/README.md) | The Python and TypeScript clients, and their gateway calls |

## Fleet

| Page | What it answers |
|---|---|
| [A fleet behind a gateway](fleet.md) | sandbox-gateway in front of one machine or many: certificates, nodes, serving it, users' keys and scopes, the security model, scheduling |
| [SSH](ssh.md) | One SSH port for every sandbox: keys, tokens, scp, sftp, rsync, port forwards, the host key, the `sandbox:ssh` rule |
| [Organizations](organizations.md) | Tenants users create and share: the model, the `X-Sandbox-Org` header, members, what is guaranteed |
| [Jobs and secrets](jobs.md) | Commands and agent runs after you have gone, batches, the secret store, notify webhooks |
| [Services and the router](services.md) | A spec and a count kept true: health, rollouts, placement, public HTTP names |
| [Operations](operations.md) | Metrics, the audit log, drain, lost nodes, revocation, changing a serving gateway |

## Reference

| Page | What it answers |
|---|---|
| [API v1](api/v1.md) | The API every mode serves: sandboxes, processes, attach, files, network policy, errors, and the gateway's endpoints |
| [Security](security/README.md) | The boundary, what crosses it and how it is checked, the profiles, and who can reach the API |
| [Secrets](security/secrets.md) | What sandbox-cli protects about a secret, what it does not, and how to make a leak cheap |

## For contributors

These are not on the site.

| Page | What it answers |
|---|---|
| [End-to-end checks](testing/end-to-end.md) | What fakes cannot prove, and the command that proves it on a real host |
| [A fleet by hand](testing/fleet-walkthrough.md) | The whole gateway on one KVM machine and a Mac, step by step |
| [Rewrite plan](rewrite/PLAN.md) | Milestones, measurements and the decisions behind the design |
| [Roadmap](roadmap/README.md) | Where earlier tasks went, and which earlier decisions were reversed |
| [Invariants](rewrite/invariants.md) | Every rule from the container design, and the test that pins it now or why it is obsolete |
| [Security audit](security/audit-2026-07-26.md), [open items](security/open-items.md) | The ledger of reproduced escapes, and the open backlog |

The documentation of the container design (0.0.1 and earlier) is in `_old/docs/`.
Fallbacks, the audit log, volumes and pools are rebuilt and described in the
[README](../README.md) and the API doc. Fleets, recovery and worktrees are not:
a sandbox has no repository, so there is no work to bring back, land or recover
on the host.
