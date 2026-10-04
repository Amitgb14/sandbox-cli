import type { Metadata } from "next";
import Link from "next/link";
import { AlertTriangle, ArrowLeft, ArrowRight, Cable, Laptop, Server, Terminal } from "lucide-react";
import { SiteHeader } from "@/components/site-header";
import { SiteFooter } from "@/components/site-footer";
import { Section, SectionHead } from "@/components/section-head";
import { CodeBlock } from "@/components/code-block";
import { type NavEntry } from "@/lib/nav";
import { INSTALL_STEP, LAUNCH_AGENT_CODE, UNINSTALL_STEPS } from "@/lib/setup";
import { DOC_URL, STUDIO_PATH } from "@/lib/site";

/**
 * The setup guide: a Mac, a Linux machine for a quick try, a Linux server, and
 * a client pointed at any of them, each from a cold machine to a sandbox that
 * ran. The landing page's setup band is the short version; this is the one to
 * follow with a terminal open.
 *
 * Mirrors docs/local-macos.md and docs/self-hosting.md, and every command here
 * is one of theirs, the install script's or the packaging's. Where a path is
 * unverified (the Mac) or degraded (Linux without root), it says so before its
 * first step.
 */

const TITLE = "Setup — sandbox-cli";
const DESCRIPTION =
  "Set up sandbox-cli on a Mac or a Linux machine: install, start sandboxd, check what it can deliver, and run a first sandbox. Plus a client pointed at a server, and what to do when a step fails.";

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
  code: "cd ~/your-project\nsandbox-cli run -- uname -a\nsandbox-cli agent claude",
  body: (
    <>
      The first run builds the image&apos;s root disk, which takes a while; later ones start fast. Your repository
      goes in as a git bundle, and its commits come back into <code>refs/sandbox/&lt;id&gt;</code> when the run
      ends. Read them with <code>git log -p HEAD..refs/sandbox/&lt;id&gt;</code>, then merge if you want them.
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
        sandbox-cli, sandboxd and the guest agent go into <code>~/.local/bin</code>, each checked against the
        release checksums. The guest agent is the Linux arm64 build: it runs inside the sandbox, mounted read-only
        from beside sandboxd, so any image works and the agent always matches the server. If the script says{" "}
        <code>~/.local/bin</code> is not on your PATH, add it as it shows.
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
        CLI&apos;s default context. It passes <code>--allow-bind</code>, so a run may mount a directory with{" "}
        <code>--bind</code>.
      </>
    ),
  },
  DOCTOR,
  FIRST_RUN,
];

const MAC_DIFFERENCES: [string, string, string][] = [
  ["Sandbox", "a VM of the container runtime", "a Firecracker microVM"],
  ["Egress", "none, or open if your policy allows it", "none or an allowlist, enforced on the host"],
  ["Workspace", "a git bundle in and out, or a bind of a directory", "a git bundle in and out"],
  ["Network policy change on a running sandbox", "no", "yes"],
];

const FIRECRACKER_FETCH = `ARCH=$(uname -m)
release_url=https://github.com/firecracker-microvm/firecracker/releases
latest=$(basename $(curl -fsSLI -o /dev/null -w '%{url_effective}' $release_url/latest))
curl -fsSL $release_url/download/$latest/firecracker-$latest-$ARCH.tgz | tar -xz
install -m 0755 release-$latest-$ARCH/firecracker-$latest-$ARCH ~/.local/bin/firecracker
install -m 0755 release-$latest-$ARCH/jailer-$latest-$ARCH ~/.local/bin/jailer`;

const LINUX_STEPS: Step[] = [
  {
    title: "Check for KVM and the tools",
    code: "ls -l /dev/kvm\nsudo usermod -aG kvm $USER     # then log out and back in\ncommand -v mkfs.ext4 ip nft",
    body: (
      <>
        x86_64 or arm64 Linux with <code>/dev/kvm</code>, which your user must be able to read and write. A cloud
        VM needs nested virtualisation for that. You also need <code>mkfs.ext4</code> (e2fsprogs 1.43 or later);{" "}
        <code>ip</code> and <code>nft</code> are only used when sandboxd runs as root.
      </>
    ),
  },
  INSTALL_STEP,
  {
    title: "Get Firecracker",
    code: FIRECRACKER_FETCH,
    body: "Firecracker and its jailer come from the project's own releases. This takes the latest for your architecture and puts both beside sandbox-cli.",
  },
  {
    title: "Get a guest kernel",
    code: "# a vmlinux built with CONFIG_IP_PNP, CONFIG_VIRTIO_VSOCKETS and overlayfs,\n# for example a CI kernel from Firecracker's getting-started guide\ninstall -D -m 0644 vmlinux ~/.local/share/sandboxd/vmlinux",
    body: (
      <>
        Each sandbox boots this kernel. It needs <code>CONFIG_IP_PNP</code>, <code>CONFIG_VIRTIO_VSOCKETS</code>{" "}
        and overlayfs; the CI kernels from Firecracker&apos;s getting-started guide have all three. Keep it anywhere
        you like and name it with <code>--kernel</code>.
      </>
    ),
  },
  {
    title: "Start sandboxd in a terminal",
    code: "sandboxd --backend firecracker \\\n  --kernel ~/.local/share/sandboxd/vmlinux \\\n  --firecracker ~/.local/bin/firecracker",
    body: (
      <>
        It listens on <code>$XDG_RUNTIME_DIR/sandboxd.sock</code>, the CLI&apos;s default local context, and its
        first line says what it will serve. Leave it running and use a second terminal for the rest.
      </>
    ),
  },
  DOCTOR,
  FIRST_RUN,
];

const SERVER_STEPS: Step[] = [
  {
    title: "Install as root, where the unit expects it",
    code: "curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh \\\n  | sudo sh -s -- --dest /usr/local/bin --no-config\nsudo install -m 0755 firecracker jailer /usr/local/bin/",
    body: (
      <>
        sandboxd and the guest agent land side by side in <code>/usr/local/bin</code>, where the systemd unit runs
        them from; the guest agent is put into every image&apos;s root disk, so it must sit beside sandboxd.{" "}
        <code>--no-config</code> because the server reads a policy file, not a client config. Fetch Firecracker as
        in the quick try above.
      </>
    ),
  },
  {
    title: "Make its directories, kernel and token",
    code: "sudo install -d -m 0700 /etc/sandboxd /etc/sandboxd/tls /var/lib/sandboxd\nsudo install -m 0644 vmlinux /var/lib/sandboxd/vmlinux\nhead -c 32 /dev/urandom | base64 | sudo tee /etc/sandboxd/token >/dev/null\nsudo chmod 600 /etc/sandboxd/token",
    body: (
      <>
        Image disks are hard-linked into each sandbox&apos;s jail, so <code>/var/lib/sandboxd</code> must be one
        filesystem; on xfs or btrfs each sandbox&apos;s disk is a reflink. The token is what clients present: at
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
        serves <code>0.0.0.0:7443</code>, and refuses a network address without both a token and TLS.
      </>
    ),
  },
  DOCTOR,
];

const CLIENT_STEPS: Step[] = [
  {
    title: "Install the client",
    code: "curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh | sh -s -- --client-only",
    body: "For a laptop that should not run VMs, an Intel Mac, or any machine talking to a server. On Windows, download the .zip from the releases page.",
  },
  {
    title: "Add the server as a context",
    code: "sandbox-cli context add box https://box.example.internal:7443 \\\n  --token-file box.token --ca box-ca.pem\nsandbox-cli context use box",
    body: "Copy the server's token into box.token, and its CA certificate if your machine does not already trust it. The token is read from the file and never appears in an argv. Contexts are how one client talks to your Mac, your server and the cloud: the commands stay the same.",
  },
  DOCTOR,
  FIRST_RUN,
  {
    title: "Open Studio",
    code: "cd ~/your-project\nsandbox-cli studio",
    body: (
      <>
        The browser view of the same sandboxes, served by sandbox-cli on a loopback port, for whichever sandboxd
        your context points at. Open the address it prints. <Link href={STUDIO_PATH}>More about Studio</Link>.
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
];

export default function SetupPage() {
  return (
    <div id="top">
      <SiteHeader nav={NAV} />
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
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
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
            yet. Expect rough edges; <a className="underline" href={DOC_URL.localMac}>docs/local-macos.md</a> lists the
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
              A bind never mounts <code className="font-mono text-foreground">/</code>, your home directory, or anything above it.
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
            an allowlist is refused, never served open. Boot, run, files, bring-back, snapshots and volumes all work. For
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
            Pools, volumes, the audit log, how the allowlist is enforced and living with a host firewall are in{" "}
            <a className="underline" href={DOC_URL.selfHosting}>docs/self-hosting.md</a>. To check the whole API
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

        <Section id="troubleshooting" tinted>
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

        <Section id="uninstall">
          <SectionHead eyebrow="uninstall" title="Taking it off again" />
          <Steps steps={UNINSTALL_STEPS} />
        </Section>
      </main>
      <SiteFooter />
    </div>
  );
}
