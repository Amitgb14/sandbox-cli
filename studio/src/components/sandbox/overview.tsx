"use client";

import { useState } from "react";
import { CopyButton } from "@/components/common/copy-button";
import { StatusBadge } from "@/components/common/status-badge";
import { Labels } from "@/components/sandbox/labels";
import { ResourceChips } from "@/components/sandbox/resource-chips";
import { StateDot } from "@/components/sandbox/state-dot";
import { formatArgv, formatDateTime, formatRelative } from "@/lib/format";
import type { Process, Sandbox } from "@/lib/types";

function idle(secs: number): string {
  if (!secs) return "never";
  if (secs % 3600 === 0) return `${secs / 3600} h`;
  if (secs % 60 === 0) return `${secs / 60} min`;
  return `${secs} s`;
}

function Section({ title, aside, children }: { title?: string; aside?: React.ReactNode; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-2.5 border-b px-5 py-4 last:border-b-0">
      {title ? (
        <div className="flex items-center justify-between gap-3">
          <h3 className="font-mono text-[11px] tracking-wider text-muted-foreground uppercase">{title}</h3>
          {aside}
        </div>
      ) : null}
      {children}
    </section>
  );
}

function Row({ k, copy, children }: { k: string; copy?: string; children: React.ReactNode }) {
  return (
    <div className="flex min-h-6 items-center justify-between gap-4 text-sm">
      <dt className="shrink-0 text-muted-foreground">{k}</dt>
      <dd className="flex min-w-0 items-center justify-end gap-1">
        <span className="min-w-0 truncate text-right">{children}</span>
        {copy ? <CopyButton value={copy} label={`Copy ${k.toLowerCase()}`} className="size-6 shrink-0" /> : null}
      </dd>
    </div>
  );
}

function Network({ sb }: { sb: Sandbox }) {
  const [open, setOpen] = useState(false);
  const allow = sb.network.allow ?? [];
  return (
    <>
      <Row k="Network">
        <span className="font-mono text-[13px]">{sb.network.mode}</span>
        {sb.network.mode === "allowlist" ? (
          <button type="button" onClick={() => setOpen(!open)} className="ml-2 text-xs text-muted-foreground underline-offset-2 hover:underline">
            {allow.length} {allow.length === 1 ? "name" : "names"}
          </button>
        ) : null}
      </Row>
      {open && allow.length ? (
        <p className="rounded-md bg-muted/40 px-2.5 py-2 font-mono text-[11px] leading-relaxed break-all text-muted-foreground">{allow.join(", ")}</p>
      ) : null}
      {sb.network.deny?.length ? <Row k="Denied">{sb.network.deny.length} names</Row> : null}
    </>
  );
}

/**
 * What a sandbox is, in sections a reader can scan down: what it is, what it
 * was given, how long it lives, how it is labelled, what is in its
 * environment and mounted in it, and what has run. Everything here is the
 * API's own record of the sandbox; resources are allocations, not live usage,
 * which sandboxd does not report.
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
  const labels = Object.keys(sb.labels ?? {}).length;
  return (
    <div className="flex flex-col">
      <Section>
        <dl className="flex flex-col gap-1.5">
          <Row k="ID" copy={sb.id}>
            <span className="font-mono text-[13px]">{sb.id}</span>
          </Row>
          <Row k="Image" copy={sb.image}>
            <span className="font-mono text-[13px]" title={sb.image}>
              {sb.image}
            </span>
          </Row>
          <Network sb={sb} />
          <Row k="Starts in">
            <span className="font-mono text-[13px]">/sandbox/home</span>
          </Row>
        </dl>
      </Section>

      <Section title="Resources">
        <ResourceChips sb={sb} />
      </Section>

      <Section title="Lifecycle">
        <dl className="flex flex-col gap-1.5">
          <Row k="State">
            <StateDot state={sb.state} />
          </Row>
          <Row k="Created">
            <span title={formatDateTime(sb.created_at)}>{formatRelative(sb.created_at)}</span>
          </Row>
          <Row k="Idle auto-stop">{idle(sb.idle_timeout_secs)}</Row>
        </dl>
      </Section>

      {labels ? (
        <Section title="Labels">
          <Labels labels={sb.labels} />
        </Section>
      ) : null}

      {sb.env_names?.length ? (
        <Section title="Environment">
          {/* Names only: the API never returns a value, and neither does this. */}
          <div className="flex flex-wrap gap-1">
            {sb.env_names.map((n) => (
              <span key={n} className="rounded-md border px-1.5 py-0.5 font-mono text-[11px]">
                {n}
              </span>
            ))}
          </div>
        </Section>
      ) : null}

      {sb.volumes?.length ? (
        <Section title="Volumes">
          <dl className="flex flex-col gap-1.5">
            {sb.volumes.map((v) => (
              <Row key={v.name} k={v.name}>
                <span className="font-mono text-[13px]">
                  {v.path}
                  {v.read_only ? " (read-only)" : ""}
                </span>
              </Row>
            ))}
          </dl>
        </Section>
      ) : null}

      <Section title="Processes" aside={processes.length ? <span className="text-xs text-muted-foreground tabular-nums">{processes.length}</span> : null}>
        {processes.length ? (
          <div className="flex flex-col divide-y">
            {[...processes].reverse().map((p) => (
              <button
                type="button"
                key={p.pid}
                onClick={() => onLogs(p.pid)}
                title="Show its logs"
                className="flex items-center gap-3 py-1.5 text-left first:pt-0 last:pb-0 hover:text-foreground"
              >
                <span className="w-8 shrink-0 font-mono text-xs text-muted-foreground tabular-nums">{p.pid}</span>
                <span className="min-w-0 flex-1 truncate font-mono text-xs">{formatArgv(p.argv)}</span>
                <StatusBadge outcome={p.state} exitCode={p.exit_code} size="sm" />
                <span className="hidden w-16 text-right text-xs text-muted-foreground sm:block">{formatRelative(p.started_at)}</span>
              </button>
            ))}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">Nothing has run in this sandbox yet.</p>
        )}
      </Section>
    </div>
  );
}
