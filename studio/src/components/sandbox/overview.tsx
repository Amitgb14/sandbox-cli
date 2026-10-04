"use client";

import { Cpu, Globe, HardDrive, ListTree, Timer } from "lucide-react";
import { StatusBadge } from "@/components/common/status-badge";
import { Card, CardContent } from "@/components/ui/card";
import { formatArgv, formatDateTime, formatRelative } from "@/lib/format";
import type { Process, Sandbox } from "@/lib/types";

function mib(n: number): string {
  return n >= 1024 ? `${+(n / 1024).toFixed(1)} GiB` : `${n} MiB`;
}

function idle(secs: number): string {
  if (!secs) return "never";
  if (secs % 3600 === 0) return `${secs / 3600} h`;
  if (secs % 60 === 0) return `${secs / 60} min`;
  return `${secs} s`;
}

function Panel({ icon: Icon, title, children }: { icon: React.ComponentType<{ className?: string }>; title: string; children: React.ReactNode }) {
  return (
    <Card className="surface-sheen gap-0 py-0">
      <CardContent className="flex flex-col gap-3 p-4">
        <h3 className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
          <Icon className="size-3.5" />
          {title}
        </h3>
        {children}
      </CardContent>
    </Card>
  );
}

function Row({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4 text-sm">
      <dt className="text-muted-foreground">{k}</dt>
      <dd className="min-w-0 truncate text-right font-mono text-[13px]">{children}</dd>
    </div>
  );
}

/**
 * What a sandbox is: what it was given, what it may reach, how long it lives,
 * what is mounted in it, and what has run. Everything here is the API's own
 * record of the sandbox; resources are allocations, not live usage, which
 * this sandboxd does not report.
 */
export function SandboxOverview({
  sb,
  processes,
  onLogs,
}: {
  sb: Sandbox;
  processes: Process[];
  onLogs: (pid: number) => void;
}) {
  return (
    <div className="flex flex-col gap-4">
      <div className="grid gap-3 md:grid-cols-3">
        <Panel icon={Cpu} title="Resources">
          <dl className="flex flex-col gap-1.5">
            <Row k="vCPU">{sb.cpus}</Row>
            <Row k="Memory">{mib(sb.memory_mb)}</Row>
            <Row k="Disk">{mib(sb.disk_mb)}</Row>
            <Row k="Image">
              <span title={sb.image}>{sb.image}</span>
            </Row>
          </dl>
        </Panel>
        <Panel icon={Globe} title="Network">
          <dl className="flex flex-col gap-1.5">
            <Row k="Mode">{sb.network.mode}</Row>
            {sb.network.mode === "allowlist" ? <Row k="Allowed">{sb.network.allow?.length ?? 0} names</Row> : null}
            {sb.network.deny?.length ? <Row k="Denied">{sb.network.deny.length} names</Row> : null}
          </dl>
          {sb.network.allow?.length ? (
            <p className="line-clamp-3 font-mono text-[11px] leading-relaxed text-muted-foreground">{sb.network.allow.join(", ")}</p>
          ) : null}
        </Panel>
        <Panel icon={Timer} title="Lifecycle">
          <dl className="flex flex-col gap-1.5">
            <Row k="State">
              <StatusBadge outcome={sb.state} size="sm" />
            </Row>
            <Row k="Started">
              <span title={formatDateTime(sb.created_at)}>{formatRelative(sb.created_at)}</span>
            </Row>
            <Row k="Idle timeout">{idle(sb.idle_timeout_secs)}</Row>
            <Row k="Starts in">/sandbox/home</Row>
          </dl>
        </Panel>
      </div>

      {(sb.volumes?.length || sb.env_names?.length) ? (
        <div className="grid gap-3 md:grid-cols-2">
          <Panel icon={HardDrive} title="Volumes">
            {sb.volumes?.length ? (
              <dl className="flex flex-col gap-1.5">
                {sb.volumes.map((v) => (
                  <Row key={v.name} k={v.name}>
                    {v.path}
                    {v.read_only ? " (read-only)" : ""}
                  </Row>
                ))}
              </dl>
            ) : (
              <p className="text-sm text-muted-foreground">None mounted.</p>
            )}
          </Panel>
          <Panel icon={ListTree} title="Environment">
            {/* Names only: the API never returns a value, and neither does this. */}
            {sb.env_names?.length ? (
              <p className="font-mono text-[12px] leading-relaxed">{sb.env_names.join("  ")}</p>
            ) : (
              <p className="text-sm text-muted-foreground">No variables set.</p>
            )}
          </Panel>
        </div>
      ) : null}

      <Panel icon={ListTree} title="Processes">
        {processes.length ? (
          <div className="flex flex-col divide-y">
            {[...processes].reverse().map((p) => (
              <div key={p.pid} className="flex items-center gap-3 py-2 first:pt-0 last:pb-0">
                <span className="w-10 shrink-0 font-mono text-xs text-muted-foreground tabular-nums">{p.pid}</span>
                <span className="min-w-0 flex-1 truncate font-mono text-xs" title={formatArgv(p.argv)}>
                  {formatArgv(p.argv)}
                </span>
                {p.tty ? <span className="text-[11px] text-muted-foreground">terminal</span> : null}
                <StatusBadge outcome={p.state} exitCode={p.exit_code} size="sm" />
                <span className="hidden w-20 text-right text-xs text-muted-foreground sm:block">{formatRelative(p.started_at)}</span>
                <button type="button" onClick={() => onLogs(p.pid)} className="text-xs text-muted-foreground hover:text-foreground">
                  logs →
                </button>
              </div>
            ))}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">Nothing has run in this sandbox yet.</p>
        )}
      </Panel>
    </div>
  );
}
