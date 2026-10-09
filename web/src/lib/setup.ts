/**
 * Setup, from a cold machine to a verified sandbox, for each place a sandbox can
 * run — and for a machine that only talks to one.
 *
 * The last step of every path is `sandbox-cli doctor`, deliberately. Installing
 * the binaries is the easy half; what *this* sandboxd can deliver — an egress
 * allowlist, snapshots, volumes — is a property of the machine and how it was
 * started, and the point of doctor is that you find out before an agent does.
 *
 * Mirrors docs/local-macos.md, docs/self-hosting.md and docs/fleet.md.
 */

import { NOT_RELEASED_YET, RELEASED, SOURCE_BUILD } from "@/lib/site";

export type SetupStep = {
  title: string;
  /** Shell to run, when the step is a command. */
  code?: string;
  body: string;
};

export type SetupPath = {
  id: string;
  label: string;
  /** What runs the sandboxes on this path. */
  engine: string;
  /**
   * Something a reader must know before following the path, shown first. The
   * macOS path carries one because its backend has not yet run on a real Mac,
   * and a setup guide is the wrong place to discover that.
   */
  caveat?: string;
  steps: SetupStep[];
};

/**
 * The install step, kept here so the setup paths and the install card agree:
 * the one-liner once a release has sandboxd (RELEASED in site.ts), a build
 * from a checkout until then.
 */
export const INSTALL_STEP: SetupStep = RELEASED
  ? {
      title: "Install",
      code: "curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh | sh",
      body: "Installs sandbox-cli, sandboxd and the guest agent beside it into ~/.local/bin, each archive verified against the release checksums, and — on a machine that has none — writes ~/.config/sandbox/config.yaml with the client settings spelled out. It installs sandboxd; it does not start it.",
    }
  : {
      title: "Build and install",
      code: `${SOURCE_BUILD}\ninstall -d ~/.local/bin\ninstall -m 0755 bin/sandbox-cli bin/sandboxd bin/sandbox-guestd ~/.local/bin/`,
      body: `${NOT_RELEASED_YET} sandbox-cli, sandboxd and the guest agent go side by side into ~/.local/bin; sandboxd is installed, not started.`,
    };

/** Installing on a server, where the unit runs everything from /usr/local/bin. */
export const SERVER_INSTALL_CODE = RELEASED
  ? "curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh \\\n  | sudo sh -s -- --dest /usr/local/bin --no-config\nsudo install -m 0755 firecracker jailer /usr/local/bin/"
  : `${SOURCE_BUILD}\nsudo install -m 0755 bin/sandboxd bin/sandbox-guestd bin/sandbox-cli /usr/local/bin/\nsudo install -m 0755 firecracker jailer /usr/local/bin/`;

/** Installing the client alone. */
export const CLIENT_INSTALL_CODE = RELEASED
  ? "curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh | sh -s -- --client-only"
  : `${SOURCE_BUILD}\ninstall -d ~/.local/bin && install -m 0755 bin/sandbox-cli ~/.local/bin/`;

/**
 * The launch agent, fetched and pointed at the installed sandboxd. The plist in
 * packaging/launchd runs /usr/local/bin/sandboxd, but the install script puts
 * it in ~/.local/bin; copying the plist as-is left a launch agent with nothing
 * to run.
 */
export const LAUNCH_AGENT_CODE = `curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/packaging/launchd/dev.sandbox.sandboxd.plist \\
  | sed "s#/usr/local/bin/sandboxd#$HOME/.local/bin/sandboxd#" \\
  > ~/Library/LaunchAgents/dev.sandbox.sandboxd.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.sandbox.sandboxd.plist`;

const DOCTOR_STEP: SetupStep = {
  title: "Ask what this sandboxd can deliver",
  code: "sandbox-cli doctor",
  body: "Prints the backend, the API version, the network policy's default and ceiling, the capabilities (egress allowlist, suspend, snapshots, volumes, audit) and the limits. A request for something missing from that list is refused, never served weaker.",
};

const FIRST_RUN_STEP: SetupStep = {
  title: "Run something",
  code: "sandbox-cli run -- uname -a\nsandbox-cli agent claude",
  body: "The first run builds the image's root disk, which takes a while; later ones start in about 80 ms on Firecracker. A sandbox starts in /sandbox/home, its own home directory, with nothing of your machine mounted in: ask the agent to clone what it needs.",
};

const STUDIO_STEP: SetupStep = {
  title: "Open Studio",
  code: "sandbox-cli studio",
  body: "The same sandboxes in a browser: launch a command or an agent, use its terminal, watch its output, files and events. Served by sandbox-cli on a loopback port for the current context; open the address it prints, token and all.",
};

export const UNINSTALL_STEPS: SetupStep[] = [
  {
    title: "Remove the binaries",
    code: "curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh | sh -s -- --uninstall",
    body: "Deletes sandbox-cli, sandboxd and the guest agent from ~/.local/bin (it checks /usr/local/bin too, and reports a file it may not remove rather than stopping), then reports what else is on disk without touching it. A running sandboxd keeps running until you stop its launch agent or unit.",
  },
  {
    title: "Then decide about logins and volumes",
    code: "sh install.sh --uninstall --purge",
    body: "--purge also deletes ~/.config/sandbox — your config and every saved agent login — and sandboxd's state directory, which holds image disks, your volumes and the audit log. It is a separate flag because signing you out of every agent and deleting your volumes is not something an uninstaller should do on its own.",
  },
];

export const SETUP_PATHS: SetupPath[] = [
  {
    id: "macos",
    label: "macOS",
    engine: "the native container runtime",
    caveat:
      "The macOS backend was written and tested on Linux against a fake runtime that runs the real guest agent. Its first runs on a real Mac (macOS 26.1) boot a sandbox in under a second, but the full check has not run yet. Expect rough edges; docs/local-macos.md lists the open points, and the Linux paths are the verified ones.",
    steps: [
      {
        title: "Have the runtime",
        code: "container system start",
        body: "macOS 26 or later, on Apple silicon. Each sandbox is a VM of the runtime with a kernel of its own.",
      },
      INSTALL_STEP,
      {
        title: "Start sandboxd as a launch agent",
        code: LAUNCH_AGENT_CODE,
        body: "The launch agent in the repository runs /usr/local/bin/sandboxd; the sed points it at the copy the script installed in ~/.local/bin, so nothing needs root. It listens on a unix socket only you can open, which is the CLI's default context. Egress here is none, or open if your policy allows it: the macOS backend does not enforce an allowlist yet, so it does not claim one.",
      },
      DOCTOR_STEP,
      FIRST_RUN_STEP,
      STUDIO_STEP,
    ],
  },
  {
    id: "linux",
    label: "Linux server",
    engine: "Firecracker",
    steps: [
      {
        title: "Have KVM, Firecracker and a guest kernel",
        code: "ls -l /dev/kvm\n# firecracker and jailer: github.com/firecracker-microvm/firecracker/releases\n# a vmlinux with IP_PNP, VIRTIO_VSOCKETS and overlayfs",
        body: "x86_64 or arm64, plus mkfs.ext4, ip and nft. The machine needs KVM: a cloud VM without nested virtualisation will not do.",
      },
      {
        title: "Give sandboxes a disk of their own",
        code: "mkfs.xfs /dev/nvme1n1   # erases it\necho \"UUID=$(blkid -s UUID -o value /dev/nvme1n1) /var/lib/sandboxd xfs defaults,noatime 0 2\" >> /etc/fstab\nmkdir -p /var/lib/sandboxd && mount /var/lib/sandboxd",
        body: "Images, sandboxes' disks, snapshots and volumes live under /var/lib/sandboxd, so a full sandbox fills that disk and not /. Mount it first, the whole directory, by UUID; the unit will not start sandboxd without it.",
      },
      {
        title: "Install, as root, where the unit expects it",
        code: SERVER_INSTALL_CODE,
        body: `${RELEASED ? "" : NOT_RELEASED_YET + " "}sandboxd, the guest agent beside it, Firecracker and its jailer go in /usr/local/bin, where packaging/systemd/sandboxd.service runs them from. The server reads its policy file, not a client config.`,
      },
      {
        title: "Run it as a service",
        code: "install -d -m 0700 /etc/sandboxd /etc/sandboxd/tls\ninstall -m 0644 vmlinux /var/lib/sandboxd/vmlinux\nhead -c 32 /dev/urandom | base64 > /etc/sandboxd/token && chmod 600 /etc/sandboxd/token\ninstall -m 0600 cert.pem key.pem /etc/sandboxd/tls/\ncp packaging/systemd/policy.example.yaml /etc/sandboxd/policy.yaml\ncp packaging/systemd/sandboxd.service /etc/systemd/system/   # set --allowed-host\nsystemctl daemon-reload && systemctl enable --now sandboxd",
        body: "As root it enforces the egress allowlist on the host and runs every VM under the jailer with a uid of its own. It refuses to listen on a network address without a token and TLS. The policy file is where the ceiling, the image list, pools and limits are set; a request can only ask for less. With --keep-sandboxes, an upgrade leaves running sandboxes running.",
      },
      {
        title: "Point your client at it",
        code: "sandbox-cli context add box https://box.example.internal:7443 \\\n  --token-file box.token --ca box-ca.pem\nsandbox-cli context use box",
        body: "From your laptop, or on the server itself. The token never appears in an argv; it is read from the file you name.",
      },
      DOCTOR_STEP,
      FIRST_RUN_STEP,
      STUDIO_STEP,
    ],
  },
  {
    id: "linux-dev",
    label: "Linux, quick try",
    engine: "Firecracker, unprivileged",
    caveat:
      "No root means no host networking: sandboxes get no network at all, and a request for an allowlist is refused. Everything else — boot, run, files, snapshots, volumes — works.",
    steps: [
      INSTALL_STEP,
      {
        title: "Start sandboxd in a terminal",
        code: "sandboxd --backend firecracker \\\n  --kernel ~/vmlinux --firecracker ~/bin/firecracker",
        body: "It listens on a unix socket under $XDG_RUNTIME_DIR, the CLI's default local context. Your user needs read-write access to /dev/kvm.",
      },
      DOCTOR_STEP,
      FIRST_RUN_STEP,
      STUDIO_STEP,
    ],
  },
  {
    id: "client",
    label: "Client only",
    engine: "a sandboxd elsewhere",
    steps: [
      {
        title: "Install the client",
        code: CLIENT_INSTALL_CODE,
        body: RELEASED
          ? "For Windows (download the .zip from the releases page), an Intel Mac, or any machine that should not run VMs itself."
          : "For an Intel Mac, or any machine that should not run VMs itself; on Windows, go build -o sandbox-cli.exe ./cmd/sandbox-cli. Until the first microVM release, build it: the published 0.0.1 client is the container design's and cannot talk to a sandboxd.",
      },
      {
        title: "Add the endpoint",
        code: "sandbox-cli context add box https://box.example.internal:7443 \\\n  --token-file box.token --ca box-ca.pem\nsandbox-cli context use box",
        body: "Contexts are how one client talks to your Mac, your server and the cloud: the commands are the same, only the context differs.",
      },
      DOCTOR_STEP,
      FIRST_RUN_STEP,
      STUDIO_STEP,
    ],
  },
];

/**
 * A fleet behind a gateway, in the commands docs/fleet.md walks through. Only
 * the setup page shows it: it is an operator's path, not a first run, so it is
 * not one of SETUP_PATHS. Every flag here is one sandbox-gateway or sandboxd
 * defines; the file paths are the packaged unit's.
 */
export const FLEET_CERTS_CODE = `curl -fsSLO https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/packaging/fleet/make-certs.sh
sh make-certs.sh -o fleet-certs \\
  -g gateway.example.internal 10.0.0.17 10.0.0.18`;

export const FLEET_NODE_CODE = `sandboxd --backend firecracker ... \\
  --listen 10.0.0.17:7443 --allowed-host 10.0.0.17 \\
  --token-file /etc/sandboxd/token \\
  --tls-cert /etc/sandboxd/tls/node-10.0.0.17.pem \\
  --tls-key /etc/sandboxd/tls/node-10.0.0.17-key.pem \\
  --client-ca /etc/sandboxd/tls/ca.pem --node-id n17`;

export const FLEET_GATEWAY_CODE = `${
  RELEASED
    ? `curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh \\
  | sudo sh -s -- --dest /usr/local/bin --no-config --client-only --with-gateway`
    : `make build   # in the checkout; then
sudo install -m 0755 bin/sandbox-gateway bin/sandbox-cli /usr/local/bin/`
}
sudo useradd --system --home-dir /var/lib/sandbox-gateway --shell /usr/sbin/nologin sandbox-gateway
sudo install -d -o sandbox-gateway -g sandbox-gateway -m 0700 /etc/sandbox-gateway /var/lib/sandbox-gateway
# certificates, node tokens and nodes.yaml into /etc/sandbox-gateway, owned by it, 0600
sudo -u sandbox-gateway sandbox-gateway --state /var/lib/sandbox-gateway/state.json \\
  keys create --user ops --scope admin
curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/packaging/systemd/sandbox-gateway.service \\
  | sudo tee /etc/systemd/system/sandbox-gateway.service >/dev/null
sudo systemctl enable --now sandbox-gateway`;

export const FLEET_USER_CODE = `sudo -u sandbox-gateway sandbox-gateway --state /var/lib/sandbox-gateway/state.json keys create \\
  --user alice --tenant team-a --scope sandbox:read --scope sandbox:create \\
  --scope sandbox:delete --scope sandbox:ssh      # with the gateway stopped, or POST /v1/admin/keys

# on alice's machine
sandbox-cli context add fleet https://gateway.example.internal:8443 \\
  --token-file fleet.key --ca ca.pem
sandbox-cli context use fleet && sandbox-cli whoami
sandbox-cli ssh demo`;

export const FLEET_USE_CODE = `sandbox-cli run --keep --name demo -- uname -a
sandbox-cli ssh demo                          # a shell; exit leaves demo running
scp -P 2222 file demo@gateway.example.internal:   # sftp, rsync and ssh -L work too
sandbox-cli ssh-access demo --ttl 10m         # a one-off ssh line, no key registered

printf %s "$TOKEN" | sandbox-cli secret set GITHUB_TOKEN   # sealed; never shown again
sandbox-cli job run -f job.yaml --wait        # a fresh sandbox per run, output kept a day
sandbox-cli service deploy -f service.yaml    # replicas kept healthy, rolled out one at a time`;

export const FLEET_ORGS_CODE = `sandbox-cli org create acme                   # needs a key with org:create; you own it
sandbox-cli org use acme                      # this context now acts in acme
sandbox-cli org members add bob               # owners only; --role owner to share ownership
sandbox-cli --org team-a ls                   # one command in another organization
sandbox-cli org ls                            # the organizations you may act in`;

export const FLEET_REVOKE_CODE = `curl -sS --cacert ca.pem -H "Authorization: Bearer $(cat ops.key)" \\
  -X DELETE https://gateway.example.internal:8443/v1/admin/keys/KEY_ID
sandbox-cli gateway audit                     # ssh.revoked, api.revoked, job.revoked`;
