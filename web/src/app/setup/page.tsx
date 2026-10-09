import type { Metadata } from "next";
import Link from "next/link";
import { AlertTriangle, ArrowLeft, ArrowRight, Cable, Laptop, Network, Server, Terminal } from "lucide-react";
import { SiteHeader } from "@/components/site-header";
import { SiteFooter } from "@/components/site-footer";
import { Section, SectionHead } from "@/components/section-head";
import { CodeBlock } from "@/components/code-block";
import { type NavEntry } from "@/lib/nav";
import {
  FLEET_CERTS_CODE,
  FLEET_GATEWAY_CODE,
  FLEET_NODE_CODE,
  FLEET_USER_CODE,
  FLEET_USE_CODE,
  FLEET_ORGS_CODE,
  FLEET_REVOKE_CODE,
  CLIENT_INSTALL_CODE,
  INSTALL_STEP,
  LAUNCH_AGENT_CODE,
  SERVER_INSTALL_CODE,
  UNINSTALL_STEPS,
} from "@/lib/setup";
import { DOC_URL, NOT_RELEASED_YET, RELEASED, STUDIO_PATH, docPath } from "@/lib/site";

/**
 * The setup guide: a Mac, a Linux machine for a quick try, a Linux server, and
 * a client pointed at any of them, each from a cold machine to a sandbox that
 * ran; then, for an operator, many servers behind one gateway. The landing page's setup band is the short version; this is the one to
 * follow with a terminal open.
 *
 * Mirrors docs/local-macos.md, docs/self-hosting.md and docs/fleet.md, and
 * every command here is one of theirs, the install script's or the packaging's. Where a path is
 * unverified (the Mac) or degraded (Linux without root), it says so before its
 * first step.
 */

const TITLE = "Setup — sandbox-cli";
const DESCRIPTION =
  "Set up sandbox-cli on a Mac or a Linux machine: install, start sandboxd, check what it can deliver, and run a first sandbox. Plus a client pointed at a server, a fleet behind a gateway, and what to do when a step fails.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  openGraph: { title: TITLE, description: DESCRIPTION, type: "article" },
  twitter: { card: "summary_large_image", title: TITLE, description: DESCRIPTION },
};

const NAV: NavEntry[] = [
  { kind: "link", href: "#mac", label: "Mac" },
  { kind: "link", href: "#linux", label: "Linux" },
  { kind: "link", href: "#server", label: "Linux server" },
  { kind: "link", href: "#client", label: "Client" },
  { kind: "link", href: "#fleet", label: "Fleet" },
  { kind: "link", href: "#troubleshooting", label: "Troubleshooting" },
];

type Step = { title: string; code?: string; lang?: "sh" | "yaml"; body: React.ReactNode };

const DOCTOR: Step = {
  title: "Ask what this sandboxd can deliver",
  code: "sandbox-cli doctor",
  body: (
    <>
      It prints the context, the backend, the API version, the network policy&apos;s default and ceiling, what it{" "}
      <em>can</em> do (egress allowlist, suspend, snapshots, volumes, audit) and its limits. A request for anything
      missing from that list is refused, never served weaker, so this is where you find out, not halfway through
      an agent&apos;s run.
    </>
  ),
};

const FIRST_RUN: Step = {
  title: "Run something",
  code: "sandbox-cli run -- uname -a\nsandbox-cli agent claude",
  body: (
    <>
      The first run builds the image&apos;s root disk, which takes a while; later ones start fast. A sandbox
      starts in <code>/sandbox/home</code>, its own home directory, and nothing on your machine is mounted in: ask
      the agent to clone what it needs. The agent&apos;s login is saved when the run ends, so you log in once.
    </>
  ),
};

const STUDIO: Step = {
  title: "Open Studio",
  code: "sandbox-cli studio\n# Studio: http://127.0.0.1:7080/#token=…",
  body: (
    <>
      The browser view of the same sandboxes: launch a command or an agent, use its terminal, watch its output,
      files and events. sandbox-cli serves it on a loopback port for whichever sandboxd your context points at, and
      holds that sandboxd&apos;s token itself; open the address it prints, token and all.{" "}
      <Link href={STUDIO_PATH}>More about Studio</Link>.
    </>
  ),
};

const MAC_STEPS: Step[] = [
  {
    title: "Start the container runtime",
    code: "container system start",
    body: "The native container runtime runs each sandbox as a lightweight VM with a kernel of its own. It needs macOS 26 or later on Apple silicon; an Intel Mac can only be a client.",
  },
  {
    title: INSTALL_STEP.title,
    code: INSTALL_STEP.code,
    body: (
      <>
        {RELEASED ? (
          <>
            sandbox-cli, sandboxd and the guest agent go into <code>~/.local/bin</code>, each checked against the
            release checksums.
          </>
        ) : (
          <>
            {NOT_RELEASED_YET} sandbox-cli, sandboxd and the guest agent go side by side into{" "}
            <code>~/.local/bin</code>.
          </>
        )}{" "}
        The guest agent is the Linux arm64 build: it runs inside the sandbox, mounted read-only
        from beside sandboxd, so any image works and the agent always matches the server. <code>~/.local/bin</code> must be on
        your PATH, or the next steps answer <code>sandboxd: command not found</code>: put{" "}
        <code>export PATH=&quot;$HOME/.local/bin:$PATH&quot;</code> in <code>~/.zshrc</code>.
      </>
    ),
  },
  {
    title: "Start sandboxd as a launch agent",
    code: LAUNCH_AGENT_CODE,
    body: (
      <>
        The launch agent in the repository runs <code>/usr/local/bin/sandboxd</code>; the <code>sed</code> points
        it at the copy in <code>~/.local/bin</code>, so nothing needs root. It starts at login, restarts if it
        exits, logs to <code>/tmp/sandboxd.log</code>, and listens on a unix socket only you can open, which is the
        CLI&apos;s context named <code>local</code>. If you have added other contexts,{" "}
        <code>sandbox-cli context use local</code> switches back to it.
      </>
    ),
  },
  DOCTOR,
  FIRST_RUN,
  STUDIO,
];

const MAC_DIFFERENCES: [string, string, string][] = [
  ["Sandbox", "a VM of the container runtime", "a Firecracker microVM"],
  ["Egress", "none, or open if your policy allows it", "none or an allowlist, enforced on the host"],
  ["Network policy change on a running sandbox", "no", "yes"],
];

const FIRECRACKER_FETCH = `ARCH=$(uname -m)
release_url=https://github.com/firecracker-microvm/firecracker/releases
latest=v1.17.0   # the version checked on a real host
curl -fsSL $release_url/download/$latest/firecracker-$latest-$ARCH.tgz | tar -xz
install -d ~/.local/bin      # as yourself, not root: this path runs sandboxd as you
install -m 0755 release-$latest-$ARCH/firecracker-$latest-$ARCH ~/.local/bin/firecracker
install -m 0755 release-$latest-$ARCH/jailer-$latest-$ARCH ~/.local/bin/jailer
~/.local/bin/firecracker --version   # Firecracker v1.17.0`;

/**
 * What the machine needs, with the versions checked on a real host
 * (docs/self-hosting.md, "Versions checked"). Both Linux paths start here.
 */
const CHECK_MACHINE_CODE = `uname -m                     # x86_64 (checked) or aarch64 (built, not yet run)
ls -l /dev/kvm               # must exist; a cloud VM needs nested virtualisation
uname -r                     # host kernel; checked: 6.12

# mkfs.ext4 (e2fsprogs 1.43+; checked 1.47.1), ip (iproute2; checked 6.17.0),
# nft (nftables; checked 1.1.5), and git, make, curl, file to build
sudo dnf install -y e2fsprogs iproute nftables git make curl file      # Fedora, RHEL and rebuilds
sudo apt-get install -y e2fsprogs iproute2 nftables git make curl file # Debian, Ubuntu (not yet checked)
go version                   # 1.25 or later builds the binaries
df -h /var/lib ~             # 20 GiB free where the state goes: /var/lib/sandboxd as root, ~/.local/share as you`;

const CHECK_MACHINE_BODY = (
  <>
    Checked on x86_64, an EL10 distribution with host kernel 6.12, xfs and firewalld; arm64 and other distributions
    are built for but not yet run. Disk: the base image takes 2.5 GiB installed, each sandbox what it writes (up
    to 10 GiB by default), each snapshot its memory plus its disk, and the build a few GiB of caches in your home;
    20 GiB free is enough to try it. If <code>/</code> is the full one, put the state on a larger filesystem; see{" "}
    <Link href={docPath("self-hosting", "what-the-machine-needs")}>What the machine needs</Link>. The host kernel
    only needs KVM, <code>tun</code> and nftables. A distribution&apos;s
    Go is often older than 1.25; <a href="https://go.dev/dl/">go.dev/dl</a> has the current one. Every version
    checked is in <Link href={docPath("self-hosting", "versions-checked")}>Versions checked</Link>.
  </>
);

const LINUX_STEPS: Step[] = [
  {
    title: "Check the machine: KVM, the tools, Go",
    code: `${CHECK_MACHINE_CODE}\nsudo usermod -aG kvm $USER   # then log out and back in: your user must open /dev/kvm`,
    body: (
      <>
        {CHECK_MACHINE_BODY} Without root, <code>ip</code> and <code>nft</code> go unused.
      </>
    ),
  },
  INSTALL_STEP,
  {
    title: "Get Firecracker 1.17.0",
    code: FIRECRACKER_FETCH,
    body: (
      <>
        Firecracker and its jailer come from the project&apos;s own releases. This takes 1.17.0, the version
        sandboxd has been checked with, for your architecture, and puts both beside sandbox-cli. It is pinned: a
        newer one may work, but has not been run. Run this path as yourself, not in a root shell: as root,{" "}
        <code>~</code> is <code>/root</code>, and a sandboxd for others belongs under systemd (the Linux server
        path below).
      </>
    ),
  },
  {
    title: "Get the guest kernel, 6.1.155",
    code: "ARCH=$(uname -m)   # on its own line: zsh escapes ( ) pasted inside a URL\nmkdir -p ~/.local/share/sandboxd\ncurl -fsSL -o ~/.local/share/sandboxd/vmlinux \\\n  https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.15/$ARCH/vmlinux-6.1.155\nfile ~/.local/share/sandboxd/vmlinux   # must say ELF 64-bit; anything else is an error page",
    body: (
      <>
        Each sandbox boots this kernel, whatever the host runs. It must be built with{" "}
        <code>CONFIG_IP_PNP</code>, <code>CONFIG_VIRTIO_VSOCKETS</code>, <code>CONFIG_OVERLAY_FS</code>,{" "}
        <code>CONFIG_EXT4_FS</code>, <code>CONFIG_VIRTIO_BLK</code> and <code>CONFIG_VIRTIO_NET</code>;
        Firecracker&apos;s CI kernel 6.1.155 has them all, for x86_64 (checked) and arm64. It is pinned: the newest
        Firecracker release does not always have CI kernels published yet. Keep it anywhere you like and name it
        with <code>--kernel</code>.
      </>
    ),
  },
  {
    title: "Start sandboxd in a terminal",
    code: "sandboxd --backend firecracker \\\n  --kernel ~/.local/share/sandboxd/vmlinux \\\n  --firecracker ~/.local/bin/firecracker",
    body: (
      <>
        It listens on <code>$XDG_RUNTIME_DIR/sandboxd.sock</code>, the CLI&apos;s context named{" "}
        <code>local</code>, and its last line names its version and what it will serve. Leave it running and use a
        second terminal for the rest. If you have added other contexts, switch back with{" "}
        <code>sandbox-cli context use local</code>; otherwise the CLI asks the one you added, and says{" "}
        <code>sandboxd did not answer</code>.
      </>
    ),
  },
  {
    title: "Or serve it on an IP address",
    code: `IP=10.0.0.17   # this machine's address, as clients dial it
curl -fsSLO https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/packaging/fleet/make-certs.sh
sh make-certs.sh -o certs $IP
sh -c 'umask 077; head -c 32 /dev/urandom | base64 > ~/sandboxd.token'
sandboxd --backend firecracker \\
  --kernel ~/.local/share/sandboxd/vmlinux --firecracker ~/.local/bin/firecracker \\
  --listen $IP:7443 --token-file ~/sandboxd.token \\
  --tls-cert certs/node-$IP.pem --tls-key certs/node-$IP-key.pem --allowed-host $IP

# on a client, with the token and certs/ca.pem copied across
sandbox-cli context add box https://10.0.0.17:7443 --token-file sandboxd.token --ca ca.pem
sandbox-cli context use box`,
    body: (
      <>
        Instead of the socket, for other machines. An address other machines can reach needs a token, TLS with a
        certificate naming that IP, and <code>--allowed-host</code>; sandboxd refuses to start without them and
        says which is missing. On <code>127.0.0.1</code> a token is enough. Loopback, the firewall and what each
        flag guards are in{" "}
        <Link href={docPath("self-hosting", "on-an-ip-address-instead-of-a-socket")}>On an IP address</Link>.
      </>
    ),
  },
  DOCTOR,
  FIRST_RUN,
  STUDIO,
];

const SERVER_STEPS: Step[] = [
  {
    title: "Check the machine: KVM, the tools, Go",
    code: CHECK_MACHINE_CODE,
    body: CHECK_MACHINE_BODY,
  },
  {
    title: "Give sandboxes a disk of their own",
    code: "lsblk -o NAME,SIZE,TYPE,FSTYPE,MOUNTPOINTS   # the new disk: no FSTYPE, no mount point (say nvme1n1)\nsudo mkfs.xfs /dev/nvme1n1                    # erases it\nsudo mkdir -p /var/lib/sandboxd\necho \"UUID=$(sudo blkid -s UUID -o value /dev/nvme1n1) /var/lib/sandboxd xfs defaults,noatime 0 2\" \\\n  | sudo tee -a /etc/fstab\nsudo systemctl daemon-reload && sudo mount /var/lib/sandboxd && sudo chmod 0700 /var/lib/sandboxd\nfindmnt /var/lib/sandboxd && df -h /var/lib/sandboxd",
    body: (
      <>
        Images, every sandbox&apos;s disk, snapshots and volumes live under <code>/var/lib/sandboxd</code>; on a
        disk of their own, a sandbox that fills its disk fills that and not <code>/</code>. Mount it before the
        next steps, which put the kernel there. By UUID, since device names can swap between boots, and without{" "}
        <code>nofail</code>: the unit has <code>RequiresMountsFor=/var/lib/sandboxd</code>, so sandboxd does not
        start on the empty directory underneath. Keep the whole directory on it: its parts are hard-linked into
        each jail. On a machine just for trying, skip this step.
      </>
    ),
  },
  {
    title: RELEASED
      ? "Install as root, with Firecracker 1.17.0"
      : "Build sandboxd, and install it as root with Firecracker 1.17.0",
    code: SERVER_INSTALL_CODE,
    body: (
      <>
        {RELEASED ? null : <>{NOT_RELEASED_YET} </>}
        sandboxd and the guest agent land side by side in <code>/usr/local/bin</code>, where the systemd unit runs
        them from; the guest agent is put into every image&apos;s root disk, so it must sit beside sandboxd.
        {RELEASED ? (
          <>
            {" "}
            <code>--no-config</code> because the server reads a policy file, not a client config.
          </>
        ) : null}{" "}
        Firecracker and its jailer come from Firecracker&apos;s own releases: 1.17.0, the version checked; see{" "}
        <Link href={docPath("self-hosting", "versions-checked")}>Versions checked</Link>.
      </>
    ),
  },
  {
    title: "Make its directories, the guest kernel (6.1.155) and the token",
    code: "sudo install -d -m 0700 /etc/sandboxd /etc/sandboxd/tls /var/lib/sandboxd\nARCH=$(uname -m)\nsudo curl -fsSL -o /var/lib/sandboxd/vmlinux \\\n  https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.15/$ARCH/vmlinux-6.1.155\nfile /var/lib/sandboxd/vmlinux   # must say ELF 64-bit\nsudo sh -c 'umask 077; head -c 32 /dev/urandom | base64 > /etc/sandboxd/token'",
    body: (
      <>
        The guest kernel is Firecracker&apos;s CI kernel 6.1.155, with every option sandboxd needs (as in the quick
        try above). Image disks are hard-linked into each sandbox&apos;s jail, so <code>/var/lib/sandboxd</code> must be one
        filesystem; on xfs or btrfs snapshot and fork copies are reflinks. The token is what clients present: at
        least 16 characters, readable by root only. sandboxd refuses a token file other users can read.
      </>
    ),
  },
  {
    title: "Add TLS and a policy",
    code: "sudo install -m 0600 cert.pem key.pem /etc/sandboxd/tls/\ncurl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/packaging/systemd/policy.example.yaml \\\n  | sudo tee /etc/sandboxd/policy.yaml >/dev/null",
    body: (
      <>
        The certificate is your CA&apos;s, for the name clients will use. The policy sets the default image, the
        resource defaults and limits, the network default and ceiling, and pools. A request can only ask for less,
        and an unknown key is an error, so a misspelt <code>celing: none</code> cannot leave the server more open
        than you think.
      </>
    ),
  },
  {
    title: "Run it as a service",
    code: "curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/packaging/systemd/sandboxd.service \\\n  | sudo tee /etc/systemd/system/sandboxd.service >/dev/null\nsudo sed -i 's/sandbox.example.internal/box.example.internal/' /etc/systemd/system/sandboxd.service\nsudo systemctl daemon-reload && sudo systemctl enable --now sandboxd\njournalctl -u sandboxd -f",
    body: (
      <>
        Set <code>--allowed-host</code> to the name clients use (the <code>sed</code> line). As root, sandboxd
        enforces the egress allowlist on the host and runs every VM under the jailer with a uid of its own. It
        serves <code>0.0.0.0:7443</code>, and refuses a network address without both a token and TLS. Add{" "}
        <code>--keep-sandboxes</code> to <code>ExecStart</code> and a restart or an upgrade leaves running
        sandboxes, and their processes, running.
      </>
    ),
  },
  DOCTOR,
  {
    title: "Install images ahead of their first sandbox",
    code: "sandbox-cli image pull ghcr.io/amitgb14/sandbox-desktop:edge   # waits; --no-wait returns at once\nsandbox-cli image ls                                            # state, size, sandboxes using each",
    body: (
      <>
        Otherwise an image is pulled and built into a root disk when a sandbox first asks for it, and that sandbox
        waits a minute or more. <code>image rm</code> frees one nothing uses. The policy&apos;s{" "}
        <code>images:</code> list limits installs as it limits runs; Studio&apos;s Images screen does the same.
      </>
    ),
  },
];

const CLIENT_STEPS: Step[] = [
  {
    title: RELEASED ? "Install the client" : "Build and install the client",
    code: CLIENT_INSTALL_CODE,
    body: RELEASED
      ? "For a laptop that should not run VMs, an Intel Mac, or any machine talking to a server. On Windows, download the .zip from the releases page."
      : `For a laptop that should not run VMs, an Intel Mac, or any machine talking to a server; on Windows, go build -o sandbox-cli.exe ./cmd/sandbox-cli. ${NOT_RELEASED_YET} The published 0.0.1 client is the container design's and cannot talk to a sandboxd.`,
  },
  {
    title: "Add the server as a context",
    code: "sandbox-cli context add box https://box.example.internal:7443 \\\n  --token-file box.token --ca box-ca.pem\nsandbox-cli context use box",
    body: "Copy the server's token into box.token, and its CA certificate if your machine does not already trust it. The token is read from the file and never appears in an argv. Contexts are how one client talks to your Mac, your server and the cloud: the commands stay the same.",
  },
  DOCTOR,
  FIRST_RUN,
  STUDIO,
];

const FLEET_STEPS: Step[] = [
  {
    title: "Make the certificates",
    code: FLEET_CERTS_CODE,
    body: (
      <>
        A private CA, the gateway&apos;s client certificate, and a server certificate per node for the address the
        gateway dials (<code>-g</code> adds one for the gateway&apos;s own API). Nodes accept a connection only with
        the gateway&apos;s certificate, and still check their token on every request. Keep <code>ca-key.pem</code>{" "}
        offline.
      </>
    ),
  },
  {
    title: "Start each node on the private network",
    code: FLEET_NODE_CODE,
    body: (
      <>
        Each node is a Linux server as above, listening only on an address the gateway shares with it.{" "}
        <code>--client-ca</code> turns on mutual TLS; <code>--node-id</code> must match the name the gateway knows
        it by, and every sandbox id the node makes carries it; <code>--allowed-host</code> is the host the gateway
        dials.
      </>
    ),
  },
  {
    title: "Install the gateway and make the first admin key",
    code: FLEET_GATEWAY_CODE,
    body: (
      <>
        <code>sandbox-gateway</code> needs no root and no KVM: the unit runs it as a user of its own with no
        capabilities. Nodes go in <code>/etc/sandbox-gateway/nodes.yaml</code> (an example is in{" "}
        <code>packaging/fleet/</code>) or are added with <code>sandbox-gateway nodes add</code>. The key&apos;s
        secret is printed once and stored only as a hash.
      </>
    ),
  },
  {
    title: "Give users keys",
    code: FLEET_USER_CODE,
    body: (
      <>
        Each user gets an API key with the scopes they need, and sees only the sandboxes they made. Users never
        hold a node&apos;s token. <code>sandbox-cli ssh</code> registers their public key and pins the
        gateway&apos;s host key, after which plain <code>ssh demo@gateway -p 2222</code> works too.
      </>
    ),
  },
  {
    title: "Use it: SSH, secrets, jobs and services",
    code: FLEET_USE_CODE,
    body: (
      <>
        One SSH port reaches every sandbox, with no tunnel or port per sandbox: the user name is the sandbox.
        Logging in needs a key with <code>sandbox:ssh</code>. A secret is sealed on the gateway and reaches only
        the runs that name it. A job runs each attempt in a fresh sandbox and keeps its output and the files it
        names; a service keeps its replicas healthy, rolls a new spec out one at a time, and with{" "}
        <code>serve --router-domain</code> answers at <code>&lt;service&gt;--&lt;org&gt;.DOMAIN</code>.
      </>
    ),
  },
  {
    title: "Organizations",
    code: FLEET_ORGS_CODE,
    body: (
      <>
        An organization is isolated: its sandboxes, secrets, jobs, services and quota are its own, and a name or
        an id from another one answers as if it did not exist. A key acts in its own organization, and in those
        its user created or was added to; nothing else, whatever a request asks for. Removing a member ends what
        they had open there at once. Studio has the same switcher at the top of its sidebar.
      </>
    ),
  },
  {
    title: "Revoke a key: access ends now",
    code: FLEET_REVOKE_CODE,
    body: (
      <>
        Revoking ends what the key already has open — SSH sessions, followed logs, attach sessions and tunnels —
        and cancels its user&apos;s running jobs when no other key of theirs is active, rather than waiting for
        them to finish. Each is recorded in the audit log, by key id and never by secret.
      </>
    ),
  },
];

const TROUBLE: { symptom: React.ReactNode; fix: React.ReactNode }[] = [
  {
    symptom: <code>firecracker backend: /dev/kvm: open /dev/kvm: permission denied</code>,
    fix: (
      <>
        Your user cannot open <code>/dev/kvm</code>. Add it to the <code>kvm</code> group and log in again. If{" "}
        <code>/dev/kvm</code> does not exist, the machine has no KVM: enable virtualisation in the firmware, or
        nested virtualisation on a cloud VM.
      </>
    ),
  },
  {
    symptom: <code>firecracker backend: mkfs.ext4 is required (e2fsprogs)</code>,
    fix: (
      <>
        Install e2fsprogs (<code>apt install e2fsprogs</code>, <code>dnf install e2fsprogs</code>).
      </>
    ),
  },
  {
    symptom: (
      <>
        A run asks for an allowlist and is refused: <code>network mode &quot;allowlist&quot; is above this
        server&apos;s ceiling (none)</code>
      </>
    ),
    fix: "sandboxd is running without root, so it has no tap devices and no network to give. That is the quick-try path working as designed. Run it as root (the Linux server path) for an enforced allowlist, or ask for --network none.",
  },
  {
    symptom: (
      <>
        sandboxd will not start: <code>refusing to serve it without --token-file</code>
      </>
    ),
    fix: "A TCP address, loopback included, is reachable by other users. Serve on the default unix socket, or pass --token-file (and TLS for anything beyond loopback).",
  },
  {
    symptom: (
      <>
        Every sandbox fails to start: <code>linking … into the jail (the jail must be on the same filesystem as the
        image cache)</code>
      </>
    ),
    fix: (
      <>
        Part of <code>/var/lib/sandboxd</code> (<code>jail/</code>, <code>rootfs/</code> or <code>volumes/</code>) is
        on a mount of its own. Its parts are hard-linked into each jail, so they must share one filesystem: mount
        the disk at the whole state directory instead. Only the audit log can go elsewhere, with{" "}
        <code>--audit-log</code>.
      </>
    ),
  },
  {
    symptom: (
      <>
        sandboxd does not start after a reboot: <code>Dependency failed for sandboxd</code>
      </>
    ),
    fix: (
      <>
        The disk for <code>/var/lib/sandboxd</code> did not mount, and the unit&apos;s{" "}
        <code>RequiresMountsFor</code> keeps sandboxd from filling <code>/</code> instead. Check{" "}
        <code>findmnt /var/lib/sandboxd</code> and the UUID in <code>/etc/fstab</code> against{" "}
        <code>blkid</code>.
      </>
    ),
  },
  {
    symptom: "sandbox-cli cannot connect to sandboxd",
    fix: (
      <>
        Check sandboxd is running and the context points at it (<code>sandbox-cli context ls</code>). On a Mac:{" "}
        <code>launchctl print gui/$(id -u)/dev.sandbox.sandboxd</code> and <code>/tmp/sandboxd.log</code>, and{" "}
        <code>container system start</code> if the runtime is not up. On a server:{" "}
        <code>journalctl -u sandboxd</code>.
      </>
    ),
  },
  {
    symptom: "On a Mac, a run asks for an allowlist and is refused",
    fix: (
      <>
        The macOS backend does not enforce an allowlist yet, so it does not claim one, and the default is{" "}
        <code>none</code>. To let sandboxes reach the network at all, set <code>ceiling: open</code> in a policy
        file passed with <code>--policy</code>, knowing that is what it means.
      </>
    ),
  },
];

function Steps({ steps }: { steps: Step[] }) {
  return (
    <ol className="flex flex-col gap-4">
      {steps.map((s, i) => (
        <li key={s.title} className="grid grid-cols-[2rem_minmax(0,1fr)] gap-x-3">
          <span className="flex size-7 items-center justify-center rounded-full border bg-card font-mono text-[0.72rem] text-muted-foreground">
            {i + 1}
          </span>
          <div className="flex min-w-0 flex-col gap-2.5 pt-0.5">
            <h3 className="text-[0.98rem] font-semibold tracking-tight">{s.title}</h3>
            {s.code ? <CodeBlock code={s.code} lang={s.lang ?? "sh"} /> : null}
            <p className="text-sm leading-relaxed text-muted-foreground [&_a]:underline [&_code]:font-mono [&_code]:text-[0.82em] [&_code]:text-foreground">
              {s.body}
            </p>
          </div>
        </li>
      ))}
    </ol>
  );
}

function Caveat({ children }: { children: React.ReactNode }) {
  return (
    <div className="mb-8 flex max-w-3xl items-start gap-3 rounded-xl border border-caution/40 bg-caution/5 px-4 py-3.5">
      <AlertTriangle className="mt-0.5 size-4 shrink-0 text-caution" />
      <p className="text-sm leading-relaxed text-muted-foreground [&_code]:font-mono [&_code]:text-foreground">
        <span className="font-medium text-foreground">Before you start:</span> {children}
      </p>
    </div>
  );
}

const PATHS = [
  { href: "#mac", icon: Laptop, title: "A Mac", what: "macOS 26+, Apple silicon. Sandboxes run on your Mac, through the native container runtime." },
  { href: "#linux", icon: Terminal, title: "Linux, quick try", what: "Any Linux with KVM, no root. Everything works except the network." },
  { href: "#server", icon: Server, title: "A Linux server", what: "systemd, root, TLS and a token. The enforced egress allowlist and the jailer." },
  { href: "#client", icon: Cable, title: "A client", what: "A laptop pointed at a server. Nothing runs locally." },
  { href: "#fleet", icon: Network, title: "A fleet", what: "Many servers behind one gateway: a key per user, ownership, SSH on one port." },
];

export default function SetupPage() {
  return (
    <div id="top">
      <SiteHeader nav={NAV} installHref="/#install" />
      <main className="flex-1">
        <Section id="start">
          <Link
            href="/"
            className="mb-8 inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
          >
            <ArrowLeft className="size-3.5" />
            sandbox-cli
          </Link>
          <SectionHead
            eyebrow="setup"
            title="From a cold machine to a sandbox that ran"
            lead="Pick where sandboxes will run. Every path installs the binaries, starts sandboxd, and ends with sandbox-cli doctor, because installing is the easy half: what a sandboxd can actually deliver depends on the machine and how it was started."
          />
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5">
            {PATHS.map((p) => (
              <a
                key={p.href}
                href={p.href}
                className="group flex flex-col gap-2 rounded-xl border bg-card p-5 transition-colors hover:border-foreground/30"
              >
                <p.icon className="size-5 text-muted-foreground" />
                <h3 className="flex items-center gap-1.5 text-[0.95rem] font-semibold tracking-tight">
                  {p.title}
                  <ArrowRight className="size-3.5 opacity-0 transition-opacity group-hover:opacity-100" />
                </h3>
                <p className="text-sm leading-relaxed text-muted-foreground">{p.what}</p>
              </a>
            ))}
          </div>
        </Section>

        <Section id="mac" tinted>
          <SectionHead
            eyebrow="macOS"
            title="On a Mac"
            lead="Each sandbox is a lightweight VM of the native container runtime, with a kernel of its own. The API is the same as on a Linux server or in the cloud, and the local endpoint is a unix socket only you can open."
          />
          <Caveat>
            the macOS backend was written and tested on Linux, against a fake runtime that runs the real guest agent.
            Its first runs on a real Mac (macOS 26.1) boot a sandbox in under a second, but the full check has not run
            yet. Expect rough edges; <Link className="underline" href={docPath("local-macos")}>Local on a Mac</Link> lists the
            open points.
          </Caveat>
          <Steps steps={MAC_STEPS} />
          <div className="mt-10 max-w-3xl">
            <h3 className="mb-3 text-[0.98rem] font-semibold tracking-tight">What is different from Linux</h3>
            <div className="overflow-x-auto rounded-xl border bg-card">
              <table className="w-full text-left text-sm">
                <thead className="border-b text-muted-foreground">
                  <tr>
                    <th className="px-4 py-2.5 font-medium" />
                    <th className="px-4 py-2.5 font-medium">macOS (local)</th>
                    <th className="px-4 py-2.5 font-medium">Linux (self-hosted, cloud)</th>
                  </tr>
                </thead>
                <tbody>
                  {MAC_DIFFERENCES.map(([k, mac, linux]) => (
                    <tr key={k} className="border-b last:border-0">
                      <td className="px-4 py-2.5 font-medium">{k}</td>
                      <td className="px-4 py-2.5 text-muted-foreground">{mac}</td>
                      <td className="px-4 py-2.5 text-muted-foreground">{linux}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <p className="mt-3 text-sm leading-relaxed text-muted-foreground">
              To stop sandboxd: <code className="font-mono text-foreground">launchctl bootout gui/$(id -u)/dev.sandbox.sandboxd</code>.
            </p>
          </div>
        </Section>

        <Section id="linux">
          <SectionHead
            eyebrow="linux, quick try"
            title="On Linux, without root"
            lead="The fastest way to a real microVM: sandboxd in a terminal, as you. Every sandbox is a Firecracker microVM with its own kernel."
          />
          <Caveat>
            without root there are no tap devices, so sandboxes get <strong>no network at all</strong> and a request for
            an allowlist is refused, never served open. Boot, run, files, snapshots and volumes all work. Run these
            steps as yourself, not in a root shell (<code>sudo su</code>): they install into your home. For
            networking, use the Linux server path.
          </Caveat>
          <Steps steps={LINUX_STEPS} />
        </Section>

        <Section id="server" tinted>
          <SectionHead
            eyebrow="linux server"
            title="On a Linux server"
            lead="One machine serves the sandbox API to your team or your laptop, with egress enforced on the host, where the guest cannot reach it. It runs as root under systemd; the VMs do not: the jailer gives each one an unprivileged uid and a chroot."
          />
          <Steps steps={SERVER_STEPS} />
          <p className="mt-8 max-w-3xl text-sm leading-relaxed text-muted-foreground">
            Before others depend on it, go through the{" "}
            <Link className="underline" href={docPath("self-hosting", "in-production")}>production checklist</Link>: room
            kept back with <code className="font-mono text-[0.82em] text-foreground">--capacity-disk-mb</code>, an alert on
            the disk&apos;s real free space, the audit log rotated, volumes backed up.{" "}
            <Link className="underline" href={docPath("self-hosting", "where-it-keeps-things")}>Where it keeps things</Link>,{" "}
            <Link className="underline" href={docPath("self-hosting", "upgrading-without-stopping-sandboxes")}>upgrading without stopping sandboxes</Link>,
            pools, volumes, the audit log, how the allowlist is enforced and living with a host firewall are in{" "}
            <Link className="underline" href={docPath("self-hosting")}>Self-hosting on Linux</Link>. To check the whole API
            against your server, run the conformance suite from a checkout:
          </p>
          <div className="mt-3 max-w-3xl">
            <CodeBlock
              code={
                "SANDBOX_CONFORMANCE_ENDPOINT=https://box.example.internal:7443 \\\nSANDBOX_CONFORMANCE_TOKEN=$(cat box.token) \\\n  go test ./internal/api/conformance -run TestEndpoint -v"
              }
            />
          </div>
        </Section>

        <Section id="client">
          <SectionHead
            eyebrow="client"
            title="A client pointed at a server"
            lead="Your laptop runs sandbox-cli and Studio; the sandboxes run on the server. Nothing about the commands changes, only the context."
          />
          <Steps steps={CLIENT_STEPS} />
        </Section>

        <Section id="fleet" tinted>
          <SectionHead
            eyebrow="fleet"
            title="Many servers behind a gateway"
            lead="sandbox-gateway serves the same API in front of any number of sandboxd nodes. Users reach only the gateway, each with an API key of their own; the gateway picks a node for every sandbox and routes every later call to it."
          />
          <Steps steps={FLEET_STEPS} />
          <p className="mt-8 max-w-3xl text-sm leading-relaxed text-muted-foreground">
            On one machine the gateway can sit beside sandboxd, reaching it on a loopback port with its token. Node
            and gateway flags, the admin API, scopes, quotas, the security model and what is not done yet are in{" "}
            <Link className="underline" href={docPath("fleet")}>the gateway docs</Link>, with{" "}
            <Link className="underline" href={docPath("ssh")}>SSH</Link>,{" "}
            <Link className="underline" href={docPath("organizations")}>organizations</Link>,{" "}
            <Link className="underline" href={docPath("jobs")}>jobs and secrets</Link>,{" "}
            <Link className="underline" href={docPath("services")}>services</Link> and{" "}
            <Link className="underline" href={docPath("operations")}>operations</Link> on pages of their own. To check a fleet end to end — one KVM
            machine and a laptop, every step with what a pass looks like — follow{" "}
            <a className="underline" href={DOC_URL.fleetWalkthrough}>the fleet walkthrough</a>.
          </p>
        </Section>

        <Section id="troubleshooting">
          <SectionHead
            eyebrow="troubleshooting"
            title="When a step fails"
            lead="sandboxd refuses rather than degrades: a control it cannot deliver stops the run with a reason. These are the reasons a new setup usually meets."
          />
          <div className="flex max-w-4xl flex-col gap-3">
            {TROUBLE.map((t, i) => (
              <div key={i} className="grid gap-2 rounded-xl border bg-card p-5 md:grid-cols-[minmax(0,2fr)_minmax(0,3fr)] md:gap-6">
                <p className="text-sm font-medium [&_code]:font-mono [&_code]:text-[0.82em]">{t.symptom}</p>
                <p className="text-sm leading-relaxed text-muted-foreground [&_code]:font-mono [&_code]:text-[0.82em] [&_code]:text-foreground">
                  {t.fix}
                </p>
              </div>
            ))}
          </div>
        </Section>

        <Section id="uninstall" tinted>
          <SectionHead eyebrow="uninstall" title="Taking it off again" />
          <Steps steps={UNINSTALL_STEPS} />
        </Section>
      </main>
      <SiteFooter />
    </div>
  );
}
