"use client";

import { FolderTree, Globe, KeyRound, Layers, ShieldCheck, Terminal } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { ArgvBlock } from "@/components/common/code-block";
import { EmptyState } from "@/components/common/empty-state";
import { useRunConfig } from "@/lib/api/queries";
import { BASELINE_EGRESS } from "@/lib/constants";
import { tildify } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { ConfigLayer, MountSpec, ResolvedConfig, Run } from "@/lib/types";

const LAYER_LABEL: Record<ConfigLayer, string> = {
  default: "built-in default",
  profile: "profile",
  user: "your config",
  project: "project .sandbox.yaml",
  explicit: "--config",
  flag: "command-line flag",
};

const MOUNT_ORIGIN_NOTE: Record<NonNullable<MountSpec["origin"]>, string> = {
  workspace: "The project itself — the one thing this run was meant to change.",
  "worktree-git":
    "The repository's shared .git, so git works inside the worktree. Its location comes from a pointer file the agent can rewrite, which is why the target has to look like a real git directory before it is mounted.",
  "persisted-home":
    "The agent's login, kept across ephemeral containers. This is where the OAuth refresh token lives — and why prod does not mount it.",
  history:
    "The host's Claude history bucket for this project, so sessions resolve on both sides. The one default that reaches a host path outside the workspace.",
  statusline:
    "A managed settings file, read-only, injecting the status-line hook. It never touches your own Claude settings.",
  share: "Asked for explicitly with --share. This deliberately widens the boundary.",
  cache: "A named cache volume, not a host path.",
};

/**
 * The resolved configuration this run actually ran under.
 *
 * Two things it makes visible that a config dump usually hides. **Which layer
 * won**, because precedence is the whole mechanism — a profile is the base layer,
 * under your own config rather than over it. And **what was refused**, because a
 * project `.sandbox.yaml` is untrusted input: silently dropping a key it asked
 * for would leave somebody debugging a setting that never applied.
 */
export function ConfigView({ run }: { run: Run }) {
  const { data, isPending } = useRunConfig(run.id);

  if (isPending) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-64 w-full" />
        <Skeleton className="h-48 w-full" />
      </div>
    );
  }

  if (!data) {
    return (
      <EmptyState
        icon={Layers}
        title="No configuration recorded"
        description="The daemon did not answer for this run."
      />
    );
  }

  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <MountsCard config={data} className="lg:col-span-2" />
      <EgressCard config={data} />
      <SecurityCard config={data} />
      <LayersCard config={data} className="lg:col-span-2" />
      <div className="lg:col-span-2">
        <ArgvBlock
          argv={data.argv}
          title={`${data.engine} argv, in BuildArgs order`}
          highlight={(flag, value) => {
            if (flag === "-v" && value && !value.includes(":/workspace")) return "widen";
            if (flag === "--cap-add") return "widen";
            if (flag === "--network" && value === "none") return "tighten";
            if (flag === "--cap-drop" || flag === "--security-opt" || flag === "--pids-limit")
              return "tighten";
            return null;
          }}
        />
        <p className="mt-2 text-xs text-muted-foreground">
          Rendered in the order the argv is built, because the order is part of the reading.
          Highlighted lines widen (red) or tighten (green) what the container can reach. This is a
          preview of what the run was started with, not something Studio hands to an engine.
        </p>
      </div>
    </div>
  );
}

function MountsCard({ config, className }: { config: ResolvedConfig; className?: string }) {
  return (
    <Card className={cn("surface-sheen gap-3", className)}>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-sm font-medium">
          <FolderTree className="size-4 text-muted-foreground" />
          Host paths in reach
          <Badge variant="outline" className="tabular-nums">
            {config.mounts.length}
          </Badge>
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-2">
        {config.mounts.map((m) => (
          <div
            key={`${m.host}:${m.container}`}
            className="flex flex-wrap items-start gap-x-3 gap-y-1 rounded-md border bg-card/40 px-2.5 py-2 text-xs"
          >
            <div className="min-w-0 flex-1 space-y-0.5">
              <p className="truncate font-mono" title={m.host}>
                {tildify(m.host)}
              </p>
              <p className="truncate font-mono text-muted-foreground" title={m.container}>
                → {m.container}
              </p>
            </div>
            <div className="flex shrink-0 items-center gap-1.5">
              <Badge
                variant="outline"
                className={cn(
                  "font-mono text-[10px]",
                  m.mode === "ro"
                    ? "border-contained/40 text-contained"
                    : "border-caution/40 text-caution",
                )}
              >
                {m.mode}
              </Badge>
              {m.origin && (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Badge variant="secondary" className="cursor-help text-[10px]">
                      {m.origin}
                    </Badge>
                  </TooltipTrigger>
                  <TooltipContent className="max-w-sm">
                    {MOUNT_ORIGIN_NOTE[m.origin]}
                  </TooltipContent>
                </Tooltip>
              )}
            </div>
          </div>
        ))}
        <p className="text-xs text-muted-foreground">
          Only declared mounts are host-connected. <code className="font-mono">HOME</code> is{" "}
          <code className="font-mono">{config.home}</code> — a fake ephemeral path — and the host
          home is never mounted.
        </p>
      </CardContent>
    </Card>
  );
}

function EgressCard({ config }: { config: ResolvedConfig }) {
  const { network } = config;
  const baseline = new Set<string>(BASELINE_EGRESS);
  const extra = network.allow.filter((d) => !baseline.has(d));

  return (
    <Card className="surface-sheen gap-3">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-sm font-medium">
          <Globe className="size-4 text-muted-foreground" />
          Egress
          <Badge variant="outline" className="font-mono text-[10px]">
            {network.mode}
          </Badge>
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3 text-xs">
        {network.mode === "none" ? (
          <p className="text-muted-foreground">
            <code className="font-mono">--network none</code>. This run asked to reach nothing.
          </p>
        ) : network.mode === "default" ? (
          <p className="text-exposed">
            Unrestricted. Any credential this agent held could have left.
          </p>
        ) : (
          <>
            <div className="flex flex-wrap items-center gap-2">
              <Badge
                variant="outline"
                className={cn(
                  "text-[10px]",
                  network.enforcement === "name"
                    ? "border-contained/40 text-contained"
                    : "border-caution/40 text-caution",
                )}
              >
                {network.enforcement === "name" ? "decided by hostname" : "matched on address"}
              </Badge>
              {network.networkName && (
                <span className="font-mono text-muted-foreground">{network.networkName}</span>
              )}
            </div>

            <div className="space-y-1.5">
              <p className="text-[11px] font-medium tracking-wide text-muted-foreground uppercase">
                Baseline {network.baseline ? "on" : "off"}
              </p>
              {network.baseline ? (
                <div className="flex flex-wrap gap-1">
                  {BASELINE_EGRESS.map((d) => (
                    <Badge key={d} variant="secondary" className="font-mono text-[10px]">
                      {d}
                    </Badge>
                  ))}
                </div>
              ) : (
                <p className="text-muted-foreground">
                  The built-in domains were dropped, so <code className="font-mono">allow</code> is
                  the whole list — including no <code className="font-mono">github.com</code>, which
                  is a write endpoint.
                </p>
              )}
            </div>

            {extra.length > 0 && (
              <div className="space-y-1.5">
                <p className="text-[11px] font-medium tracking-wide text-muted-foreground uppercase">
                  Added for this run
                </p>
                <div className="flex flex-wrap gap-1">
                  {extra.map((d) => (
                    <Badge
                      key={d}
                      variant="outline"
                      className="border-caution/40 font-mono text-[10px] text-caution"
                    >
                      {d}
                    </Badge>
                  ))}
                </div>
              </div>
            )}

            {network.ingressPorts && network.ingressPorts.length > 0 && (
              <p className="text-muted-foreground">
                Ingress is default-deny except loopback, established replies, and{" "}
                <span className="font-mono">{network.ingressPorts.join(", ")}</span> — publishing a
                port <em>is</em> an explicit request for ingress.
              </p>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}

function SecurityCard({ config }: { config: ResolvedConfig }) {
  const s = config.security;
  return (
    <Card className="surface-sheen gap-3">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-sm font-medium">
          <ShieldCheck className="size-4 text-muted-foreground" />
          Container
        </CardTitle>
      </CardHeader>
      <CardContent>
        <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-xs">
          <Field label="User" value={s.user} />
          <Field label="Image" value={config.image} mono />
          <Field label="Workdir" value={config.workdir} mono />
          <Field label="HOME" value={config.home} mono />
          <Field label="no-new-privileges" value={String(s.noNewPrivileges)} />
          <Field label="Capabilities dropped" value={s.capDrop.join(", ")} />
          <Field
            label="Capabilities added"
            value={s.capAdd.length ? s.capAdd.join(", ") : "none"}
            tone={s.capAdd.length ? "text-caution" : undefined}
          />
          <Field label="pids-limit" value={String(s.pidsLimit)} />
          <Field label="Memory" value={s.memory || "unlimited"} />
          <Field label="CPUs" value={s.cpus || "unlimited"} />
          <Field label="Seccomp" value={s.seccomp || "docker default profile"} />
          <Field label="Engine" value={config.engine} />
        </dl>

        <div className="mt-3 space-y-2 border-t pt-3 text-xs">
          <div className="flex items-start gap-2">
            <KeyRound className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
            <p className="text-muted-foreground">
              {config.persistAuth
                ? "The persisted agent HOME was mounted, so the OAuth refresh token was readable by the agent."
                : "The persisted agent HOME was not mounted — there was no refresh token to steal."}
            </p>
          </div>
          <div className="flex items-start gap-2">
            <Terminal className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
            <p className="text-muted-foreground">
              Forwarded host variables:{" "}
              {config.envAllow.length === 0 ? (
                "none"
              ) : (
                <span className="font-mono">{config.envAllow.join(", ")}</span>
              )}
              . Recorded by name only — a run log is a file, and there is nowhere in it to put a
              value.
            </p>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

function Field({
  label,
  value,
  mono,
  tone,
}: {
  label: string;
  value: string;
  mono?: boolean;
  tone?: string;
}) {
  return (
    <div className="min-w-0 space-y-0.5">
      <dt className="truncate text-muted-foreground">{label}</dt>
      <dd className={cn("truncate", mono && "font-mono", tone)} title={value}>
        {value}
      </dd>
    </div>
  );
}

function LayersCard({ config, className }: { config: ResolvedConfig; className?: string }) {
  const refused = config.fields.filter((f) => f.refusedFrom);
  return (
    <Card className={cn("surface-sheen gap-3", className)}>
      <CardHeader className="gap-1">
        <CardTitle className="flex items-center gap-2 text-sm font-medium">
          <Layers className="size-4 text-muted-foreground" />
          Which layer won
        </CardTitle>
        <p className="text-xs text-muted-foreground">
          Precedence, later wins: built-in default → profile → your config → the project&apos;s{" "}
          <code className="font-mono">.sandbox.yaml</code> → an explicit{" "}
          <code className="font-mono">--config</code> → flags. A profile sits{" "}
          <em>under</em> your config, not over it — a profile you cannot adjust gets abandoned.
        </p>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="overflow-hidden rounded-md border">
          <table className="w-full text-xs">
            <thead className="bg-muted/40">
              <tr className="text-left">
                <th className="px-2.5 py-1.5 font-medium">Key</th>
                <th className="px-2.5 py-1.5 font-medium">Value</th>
                <th className="px-2.5 py-1.5 font-medium">Set by</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {config.fields.map((f) => (
                <tr key={f.key} className="hover:bg-accent/40">
                  <td className="px-2.5 py-1.5 font-mono">{f.key}</td>
                  <td className="px-2.5 py-1.5 font-mono text-muted-foreground">{f.value}</td>
                  <td className="px-2.5 py-1.5">
                    <Badge variant="secondary" className="text-[10px]">
                      {LAYER_LABEL[f.layer]}
                    </Badge>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        {refused.length > 0 && (
          <div className="rounded-md border border-caution/30 bg-caution/5 p-3 text-xs">
            <p className="font-medium text-caution">
              {refused.length} {refused.length === 1 ? "key was" : "keys were"} refused from the
              project config
            </p>
            <p className="mt-1 text-muted-foreground">
              A project <code className="font-mono">.sandbox.yaml</code> is untrusted input: it may
              tighten the boundary and never loosen it. These keys were declared there and did not
              apply —{" "}
              <span className="font-mono">{refused.map((f) => f.key).join(", ")}</span>. The escape
              hatches are your own config and an explicit{" "}
              <code className="font-mono">--config</code>, where typing the path is the deliberate
              act discovery never involves.
            </p>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
