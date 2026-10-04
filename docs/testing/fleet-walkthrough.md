# A fleet by hand

A walkthrough of everything the gateway does, on real hardware: one Linux
machine with KVM runs a `sandboxd` node and `sandbox-gateway`, and a Mac (or any
other machine) is the client. It is the same shape as a fleet of hundreds of
nodes, with one. [fleet.md](../fleet.md) explains each piece; this page only
runs them in order and says what a pass looks like.

Several steps prove rows of [end-to-end.md](end-to-end.md): sandboxes and SSH
are rows 37 and 39, jobs 41, services 42, revocation 43 and Studio 44. Record
results there.

Throughout, `LINUX_IP` is the Linux machine's address as the Mac reaches it.

## 0. Build (Linux, in a checkout of `main`)

```sh
make studio                      # Studio's UI, embedded into sandbox-cli
go build -o bin/sandboxd        ./cmd/sandboxd
go build -o bin/sandbox-gateway ./cmd/sandbox-gateway
go build -o bin/sandbox-cli     ./cmd/sandbox-cli
GOOS=darwin GOARCH=arm64 go build -o bin/sandbox-cli-darwin ./cmd/sandbox-cli
```

Copy `bin/sandbox-cli-darwin` to the Mac as `sandbox-cli`, somewhere on `PATH`.
The base image needs rebuilding with `openssh-sftp-server` for plain `scp`;
until then use `scp -O`, which copies over the session instead.

## 1. The node (Linux, first terminal)

`sandboxd` on loopback, with a bearer token and a node name. Only the gateway
talks to it, so it needs no TLS: a node may be plain `http://` on loopback only.

```sh
mkdir -p ~/gw && chmod 700 ~/gw
head -c 32 /dev/urandom | base64 > ~/gw/node.token && chmod 600 ~/gw/node.token

sudo bin/sandboxd --backend firecracker --kernel /var/lib/sandboxd/vmlinux \
  --firecracker /usr/local/bin/firecracker --jailer /usr/local/bin/jailer \
  --policy /etc/sandboxd/policy.yaml \
  --listen 127.0.0.1:7443 --token-file ~/gw/node.token --node-id local
```

Use the kernel, firecracker and policy paths of your
[self-hosting](../self-hosting.md) setup.

## 2. The gateway (Linux, second terminal)

```sh
# a certificate for the gateway's API, so the Mac can reach it over TLS
sh packaging/fleet/make-certs.sh -o ~/gw/certs -g LINUX_IP 127.0.0.1
# the key that seals secrets and jobs' environments
head -c 32 /dev/urandom > ~/gw/secrets.key && chmod 600 ~/gw/secrets.key

GW="bin/sandbox-gateway --state $HOME/gw/state.json"
$GW nodes add local http://127.0.0.1:7443 --token-file ~/gw/node.token

# three users; each secret (sgk_…) is printed once: save each
$GW keys create --user ops --scope admin                                   # ops.key
$GW keys create --user alice --tenant team-a --scope sandbox:read \
  --scope sandbox:create --scope sandbox:delete --scope sandbox:ssh \
  --scope secrets:write                                                    # alice.key
$GW keys create --user bob --tenant team-b --scope sandbox:read            # bob.key, read-only

$GW serve --listen 0.0.0.0:8443 \
  --tls-cert ~/gw/certs/gateway.pem --tls-key ~/gw/certs/gateway-key.pem \
  --ssh-listen 0.0.0.0:2222 --ssh-public-host LINUX_IP \
  --secrets-key-file ~/gw/secrets.key \
  --router-listen 127.0.0.1:8080 --router-domain apps.test
```

**Pass:** the first log line says one node answers. `keys` and `nodes` refuse
while `serve` runs (it holds the state file); stop it to change them, or use
the admin API. Copy `~/gw/certs/ca.pem` to the Mac.

## 3. A context per user (Mac)

```sh
umask 077; mkdir -p ~/.config/sandbox
printf '%s\n' 'sgk_…' > ~/.config/sandbox/alice.key     # likewise bob.key and ops.key
for u in alice bob ops; do
  sandbox-cli context add $u https://LINUX_IP:8443 \
    --token-file ~/.config/sandbox/$u.key --ca ca.pem
done
sandbox-cli context use alice
sandbox-cli whoami
```

**Pass:** `whoami` prints alice, team-a, her key id and her five scopes.
`SANDBOX_CONTEXT=bob sandbox-cli …` runs any command below as another user.

## 4. Sandboxes and SSH (alice)

```sh
sandbox-cli run --keep --name demo -- uname -a      # a real microVM on the node
sandbox-cli ls                                      # demo, with an sbx_local_… id
sandbox-cli ssh demo                                # an interactive shell; exit leaves demo running
sandbox-cli ssh demo -- uname -a
sandbox-cli ssh-access demo --ttl 10m               # prints a one-off ssh line: run it
scp -O -P 2222 -o UserKnownHostsFile=~/.config/sandbox/known_hosts somefile demo@LINUX_IP:
ssh -p 2222 -o UserKnownHostsFile=~/.config/sandbox/known_hosts \
  -L 9000:127.0.0.1:8000 demo@LINUX_IP              # then curl localhost:9000 on the Mac
```

For the forward, start something on port 8000 in the sandbox first:
`sandbox-cli ssh demo -- python3 -m http.server 8000 &`.

**Pass:** every command works with no port opened per sandbox — only 8443 and
2222 on the Linux machine.

## 5. Isolation and scopes (bob, read-only)

```sh
SANDBOX_CONTEXT=bob sandbox-cli ls               # empty: demo is alice's
SANDBOX_CONTEXT=bob sandbox-cli ssh demo         # refused
SANDBOX_CONTEXT=bob sandbox-cli run -- true      # refused: no sandbox:create
SANDBOX_CONTEXT=ops sandbox-cli ls               # an admin sees every sandbox, demo included
```

**Pass:** bob cannot see, reach or create anything; another user's sandbox
answers as if it did not exist.

## 6. Secrets and jobs (alice)

```sh
printf 'hello-secret' | sandbox-cli secret set GREETING
sandbox-cli secret ls                            # the name, never the value

cat > job.yaml <<'YAML'
command: [sh, -c, 'echo "$GREETING" > /sandbox/home/out.txt; echo done']
secrets: [GREETING]
keep: {files: [/sandbox/home/out.txt]}
YAML
sandbox-cli job run -f job.yaml --wait
sandbox-cli job ls
sandbox-cli job output JOB_ID 0                              # done
sandbox-cli job output JOB_ID 0 --file /sandbox/home/out.txt # hello-secret
```

**Pass:** the job ran in a fresh microVM, the secret reached it, and its output
and file outlive the sandbox. The value appears in no gateway log line.

## 7. A service and the router (alice)

```sh
cat > svc.yaml <<'YAML'
name: web
command: [python3, -m, http.server, "8080"]
replicas: 2
port: 8080
health: {http: /, every_secs: 5}
public: true
YAML
sandbox-cli service deploy -f svc.yaml
sandbox-cli service get web                       # two replicas, healthy
```

Terminate one replica's sandbox (`sandbox-cli kill SANDBOX`): `service get` shows
it replaced within a health interval or two. Then, on the Linux machine, reach
it through the router by name — alice's tenant is in the host name:

```sh
curl -H 'Host: web--team-a.apps.test' http://127.0.0.1:8080/
```

And on the Mac:

```sh
sandbox-cli service scale web 3
sandbox-cli service rm web                        # terminates the replicas
```

**Pass:** the router answers with a replica's directory listing; a killed
replica comes back; scale and remove take effect.

## 8. Studio (Mac)

One Studio per user, each on its own port:

```sh
sandbox-cli studio --context alice --port 7091
sandbox-cli studio --context bob   --port 7092
sandbox-cli studio --context ops   --port 7093
```

**Pass:**

- alice sees Jobs, Services, Secrets, SSH and Account beside the sandbox
  screens, and no admin screens. Typing `/admin/nodes` into the address bar
  shows *Not available*.
- bob sees the same screens with no Create, Terminate, Terminal or secret-set
  actions.
- ops also sees Nodes, Lost sandboxes, Users & keys and Audit. A key issued
  there shows its secret once.
- No page shows the node's endpoint or a secret's value.

## 9. Revocation

1. As alice, hold `sandbox-cli ssh demo` open in one terminal; in a second,
   `sandbox-cli run -d --name follow -- sleep 600` and then
   `sandbox-cli logs follow`, left open; and in another start a long job:
   `command: [sleep, "600"]`.
2. Revoke alice's key with the admin key. Her key id is in
   `sandbox-cli whoami`:

   ```sh
   curl -sS --cacert ca.pem -H "Authorization: Bearer $(cat ~/.config/sandbox/ops.key)" \
     -X DELETE https://LINUX_IP:8443/v1/admin/keys/KEY_ID
   ```

**Pass:**

- the SSH session ends at once, and so does `sandbox-cli logs follow`;
- the job's sandbox is gone from `SANDBOX_CONTEXT=ops sandbox-cli ls`, and
  `SANDBOX_CONTEXT=ops sandbox-cli gateway audit` shows `api.revoked` (the
  followed output), `ssh.revoked` and `job.revoked`;
- `sandbox-cli ls` as alice is refused with 401;
- a read-only key gets no SSH: `SANDBOX_CONTEXT=bob sandbox-cli ssh-key add`
  and `ssh-access` are refused for bob.

## 10. Draining a node (ops)

```sh
SANDBOX_CONTEXT=ops sandbox-cli gateway drain local    # cordons it and counts what still runs
SANDBOX_CONTEXT=ops sandbox-cli run -- true            # refused: no node is taking sandboxes
```

```sh
SANDBOX_CONTEXT=ops sandbox-cli gateway nodes            # local: cordoned
SANDBOX_CONTEXT=ops sandbox-cli gateway uncordon local   # takes sandboxes again
```

## 11. A hosted dashboard build

On Linux, build a Mac `sandbox-cli` whose Studio has no admin screens:

```sh
NEXT_PUBLIC_STUDIO_ADMIN=off make studio
GOOS=darwin GOARCH=arm64 go build -o bin/sandbox-cli-hosted ./cmd/sandbox-cli
make studio                      # back to the default UI for the next build
```

Copy it to the Mac and run `./sandbox-cli-hosted studio --context ops --port 7094`.

**Pass:** even with an admin key, Studio has no admin screens: the build does
not contain them.

## Cleaning up

Stop `serve` and `sandboxd`, then `rm -rf ~/gw`. The sandboxes went with
`sandboxd`'s own state; nothing else on the machine was changed.
