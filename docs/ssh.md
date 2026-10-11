# SSH on one port

A gateway serves one SSH server for every sandbox in the fleet:
`ssh SANDBOX@gateway`. Nothing is opened per sandbox — a fleet of any size
needs the API's port and this one. The SSH user name says which sandbox; the
gateway checks who may log in, and the session runs in the sandbox through
its node's API, the same way `sandbox-cli shell` does.

This page is the user's and the operator's view of it. Setting up the gateway
itself is [fleet.md](fleet.md); the endpoints are in
[api/v1.md](api/v1.md#gateway-only-endpoints).

## Serving it

SSH is off until the gateway is given an address for it:

```sh
sudo -u sandbox-gateway sandbox-gateway serve --state /var/lib/sandbox-gateway/state.json \
  --listen 0.0.0.0:8443 \
  --tls-cert /etc/sandbox-gateway/tls/gateway.pem \
  --tls-key /etc/sandbox-gateway/tls/gateway-key.pem \
  --node-config /etc/sandbox-gateway/nodes.yaml \
  --ssh-listen 0.0.0.0:2222 --ssh-public-host gateway.example.internal
```

| Flag | Default | |
|---|---|---|
| `--ssh-listen HOST:PORT` | off | the SSH server. |
| `--ssh-host-key FILE` | `ssh_host_ed25519_key` beside the state file | created 0600 when missing; one key for the whole fleet. |
| `--ssh-public-host`, `--ssh-public-port` | the `--ssh-listen` host and port | what clients are told to connect to. Required when listening on `0.0.0.0` or behind a load balancer. |

To serve SSH on port 22, give the unit
`AmbientCapabilities=CAP_NET_BIND_SERVICE` (it is in the unit, commented out),
and move the host's own sshd elsewhere first.

`GET /v1/ssh`, with any key, says where the server listens and which host key
to pin; it is `404 unsupported` on a gateway that serves no SSH. The host key
is refused if other users can read it or if it is a symlink. Losing it makes
every user's `ssh` warn that the host changed, so back it up with the state
file ([fleet.md](fleet.md#back-up)).

## Logging in

```sh
sandbox-cli ssh demo                 # registers ~/.ssh/id_ed25519.pub if needed, pins the host key, runs ssh
sandbox-cli ssh demo -- uname -a

sandbox-cli ssh-key add              # or register a key yourself (--sandbox NAME limits it to one)
sandbox-cli ssh-key list
ssh -p 2222 -o UserKnownHostsFile=~/.config/sandbox/known_hosts demo@gateway.example.internal
scp -P 2222 -o UserKnownHostsFile=~/.config/sandbox/known_hosts file demo@gateway.example.internal:

sandbox-cli ssh-access demo --ttl 10m   # prints a one-off `ssh -p 2222 sgt_…@gateway` line
```

The SSH user name is the sandbox (its id, or a name among your own). Logins are
by a registered public key, or by an `ssh-access` token as the user name; there
are no passwords.

`sandbox-cli ssh` does the setup once: it registers your public key if it is
not already, pins the gateway's host key, and runs your own `ssh` client
(`ssh -p PORT SANDBOX@HOST`), whose exit status it returns. With a command
after `--`, ssh runs it instead of a login shell. The key is `--identity` (a
`.pub` file, or a private key with its `.pub` beside it), else the first of
`~/.ssh/id_ed25519.pub`, `id_ecdsa.pub` and `id_rsa.pub`. The CLI reads only
the public half; ssh does the authentication. Afterwards plain `ssh` works
too.

Against a plain `sandboxd`, which has no SSH server, `sandbox-cli ssh` says so
and opens the same shell (or runs the command) through the API instead.

## Keys

```sh
sandbox-cli ssh-key add                      # the default key, for all your sandboxes
sandbox-cli ssh-key add ~/.ssh/work.pub --sandbox demo   # for one sandbox only
sandbox-cli ssh-key list
sandbox-cli ssh-key rm ssh_…                  # open connections made with it close
```

A key is one authorized_keys line, with no options (`command=`, `from=` …).
Accepted types are ed25519, ECDSA P-256/384/521, the two security-key types,
and RSA of 2048 bits or more. `--sandbox` limits a key to one sandbox,
resolved to its id when the key is registered. A key already registered for
the same sandbox, or registered by another user, is refused.

An SSH key is its user's, kept under their own tenant whatever organisation
registered it ([organizations.md](organizations.md#ssh)). An admin lists and
removes any user's SSH keys with `GET /v1/admin/ssh-keys?user=U` and
`DELETE /v1/admin/ssh-keys/{id}`.

## Access tokens

```sh
sandbox-cli ssh-access demo            # 15 minutes
sandbox-cli ssh-access demo --ttl 5m
```

`ssh-access` asks the gateway for a token that logs in to one sandbox until it
expires, and prints the `ssh` command that uses it. The token is the SSH user
name and the whole credential: anyone holding the line can log in until it
expires (15 minutes by default, at most 24 hours), so hand it only to whoever
should have that access. It is returned once and stored only as a hash. It
needs no registered key, which makes it the way to give a machine or a
colleague one sandbox for a while.

## Copying files and forwarding ports

A session is a shell, a command, sftp (so `scp` works) or a local forward
(`ssh -L`) to a port on the sandbox's own loopback:

```sh
KH="-o UserKnownHostsFile=~/.config/sandbox/known_hosts"
scp -P 2222 $KH report.txt demo@gateway.example.internal:          # into the sandbox's home
sftp -P 2222 $KH demo@gateway.example.internal
rsync -e "ssh -p 2222 $KH" -a src/ demo@gateway.example.internal:src/   # needs rsync in the image
ssh -p 2222 $KH -L 9000:127.0.0.1:8000 demo@gateway.example.internal     # localhost:9000 -> the sandbox's :8000
```

A command is run through the guest's shell, as sshd runs one, which is what
`scp -O`, `rsync` and git over ssh expect. sftp is the guest's `sftp-server`:
the base image installs `openssh-sftp-server`, and `scp` uses sftp by default
since OpenSSH 9.0. On an image without it, `scp -O` copies over a plain
session instead.

Agent forwarding, X11 and remote forwards (`-R`) are refused: the guest never
reaches the gateway's network, and a local forward goes only to the
sandbox's own loopback. A session accepts only `TERM`, `LANG` and `LC_*` from
the client's environment.

## Pinning the host key

`sandbox-cli ssh` writes the gateway's host key into
`~/.config/sandbox/known_hosts`, replacing any entry for that host and port
and keeping every other line. The key comes from `GET /v1/ssh`, over the
context's authenticated and verified HTTPS: the same trust that already
decides what the API answers. Point plain `ssh` at that file with
`-o UserKnownHostsFile=~/.config/sandbox/known_hosts`, or copy the line into
your own `known_hosts`.

The gateway has one host key for the whole fleet, ed25519 only. A host name
the gateway reports is checked before it is written into `known_hosts` or
`ssh`'s argv: no spaces, commas, wildcards, `!` or `@`, and no leading `-`.

## The `sandbox:ssh` rule

SSH is a scope of its own, and access ends with it:

- **Registering keys, issuing tokens and logging in** need `sandbox:ssh`
  ([fleet.md](fleet.md#giving-users-keys)). A user left with read-only keys
  cannot open a shell.
- **A login, by SSH key or token, needs its user to hold an active API key
  with `sandbox:ssh` at the time**, in that tenant — not only when the SSH key
  was registered.
- **An open connection ends when they no longer do.** Revoking a user's last
  such key closes every SSH connection they have open, its sessions and
  forwards with it, and hangs up the processes they ran. A connection made
  with an `ssh-access` token is held to the same check. Removing an SSH key
  closes the connections that logged in with it. The gateway also rechecks
  every 30 seconds, for a state file changed while it was stopped
  ([operations.md](operations.md#revoking)).

The audit record has `ssh.login` for each login and `ssh.revoked` (result
`closed`) for each connection revocation ended, with the credential's key id
and fingerprint — never a token, since a token login's user name is the token.

## From an SDK

The Python and TypeScript SDKs take the gateway's URL and the key as their
token, and have the same SSH calls: `ssh_info`/`sshInfo`,
`add_ssh_key`/`addSSHKey`, `ssh_keys`/`sshKeys`, `remove_ssh_key`/`removeSSHKey`
and `ssh_access`/`sshAccess` ([sdk/README.md](../sdk/README.md)).

## Checking it works

`sandbox-cli ssh` against a real gateway and OpenSSH is row 37 of
[testing/end-to-end.md](testing/end-to-end.md);
[testing/fleet-walkthrough.md](testing/fleet-walkthrough.md#4-sandboxes-and-ssh-alice)
runs every command on this page against one node.
