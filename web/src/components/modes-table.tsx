import { ArrowUpRight, Cloud, Laptop, Server } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { DOC_URL } from "@/lib/site";
import { cn } from "@/lib/utils";

/**
 * The three places a sandbox runs, side by side. The point of the layout is
 * the last row: the API is the same in all three, so moving between them is a
 * `sandbox-cli context use`, not a migration.
 *
 * Status is stated per mode and plainly. The Linux mode is the verified one; the
 * Mac backend has not yet run on a real Mac; the cloud is planned and its
 * terms are undecided. A page that showed three equal cards would be making a
 * claim the repository does not.
 */
const MODES = [
  {
    id: "local",
    icon: Laptop,
    name: "Local",
    where: "your Mac",
    status: { label: "not yet run on a real Mac", tone: "caution" as const },
    backend: "the native container runtime — a VM per sandbox",
    needs: "macOS 26 on Apple silicon",
    good: "Iterating on an agent or a harness: offline, no cost per second, a directory mounted when you want one (--bind).",
    guide: { label: "docs/local-macos.md", href: DOC_URL.localMac },
  },
  {
    id: "self",
    icon: Server,
    name: "Self-hosted",
    where: "a Linux machine you control",
    status: { label: "verified on KVM", tone: "contained" as const },
    backend: "Firecracker microVMs, egress enforced on the host",
    needs: "Linux with /dev/kvm, x86_64 or arm64",
    good: "Code that cannot leave the building, a team's shared box, fleets of agents: nothing phones home.",
    guide: { label: "docs/self-hosting.md", href: DOC_URL.selfHosting },
  },
  {
    id: "cloud",
    icon: Cloud,
    name: "Cloud",
    where: "hosted",
    status: { label: "planned", tone: "muted" as const },
    backend: "the same Firecracker nodes, run for you",
    needs: "an API key",
    good: "Bursts bigger than a laptop, and clients that cannot run VMs at all.",
    guide: { label: "docs/rewrite/PLAN.md", href: DOC_URL.plan },
  },
];

const TONE = {
  contained: "border-contained/30 bg-contained-soft text-contained",
  caution: "border-caution/40 bg-caution/5 text-caution",
  muted: "border-border text-muted-foreground",
};

export function ModesTable({ className }: { className?: string }) {
  return (
    <div className={cn("flex flex-col gap-4", className)}>
      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        {MODES.map((m) => (
          <div key={m.id} className="flex flex-col gap-3 rounded-2xl border bg-card p-5">
            <div className="flex items-start justify-between gap-3">
              <div className="flex items-center gap-2.5">
                <span className="flex size-8 items-center justify-center rounded-lg border bg-surface">
                  <m.icon className="size-4" />
                </span>
                <div>
                  <h3 className="text-base font-semibold tracking-tight">{m.name}</h3>
                  <p className="text-xs text-muted-foreground">{m.where}</p>
                </div>
              </div>
              <Badge variant="outline" className={cn("text-[0.62rem]", TONE[m.status.tone])}>
                {m.status.label}
              </Badge>
            </div>
            <dl className="flex flex-col gap-2 text-[0.82rem]">
              <div>
                <dt className="eyebrow mb-0.5">runs on</dt>
                <dd className="text-muted-foreground">{m.backend}</dd>
              </div>
              <div>
                <dt className="eyebrow mb-0.5">needs</dt>
                <dd className="text-muted-foreground">{m.needs}</dd>
              </div>
              <div>
                <dt className="eyebrow mb-0.5">good for</dt>
                <dd className="text-muted-foreground">{m.good}</dd>
              </div>
            </dl>
            <a
              href={m.guide.href}
              target="_blank"
              rel="noopener noreferrer"
              className="mt-auto inline-flex items-center gap-1 font-mono text-xs text-muted-foreground transition-colors hover:text-foreground"
            >
              {m.guide.label} <ArrowUpRight className="size-3" />
            </a>
          </div>
        ))}
      </div>
      <div className="rounded-2xl border bg-surface px-5 py-4 text-[0.85rem] leading-relaxed text-muted-foreground">
        <span className="font-medium text-foreground">One API in all three.</span> The CLI, the SDKs
        and Studio cannot tell which they are talking to beyond what{" "}
        <code className="font-mono text-foreground">GET /v1/capabilities</code>{" "}
        says, and a
        conformance suite run against an endpoint is what &ldquo;the same&rdquo; means. Moving
        between them is{" "}
        <code className="font-mono text-foreground">sandbox-cli context use</code>, not a
        migration.
      </div>
    </div>
  );
}
