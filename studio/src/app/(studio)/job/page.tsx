"use client";

import { Suspense, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Download } from "lucide-react";
import { toast } from "sonner";
import { PageHeader, SectionHeader } from "@/components/common/page-header";
import { StatusBadge } from "@/components/common/status-badge";
import { Gate } from "@/components/shell/gate";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { decodeB64 } from "@/lib/api/client";
import { downloadJobFile, useCancelJob, useJob, useJobOutput } from "@/lib/api/gateway";
import { useCan } from "@/lib/caller";
import { DASH, formatArgv, formatBytes, formatDateTime, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { Job, JobRun } from "@/lib/types";

/**
 * One job: what it runs, each run's state, and what was kept of each — its
 * output and files — after the run's sandbox is gone. Output is rendered as
 * text, never as markup: it is whatever the command printed.
 */
function JobDetail() {
  const id = useSearchParams().get("id") ?? "";
  const can = useCan();
  const { data: job, error } = useJob(id);
  const cancel = useCancelJob();
  const [n, setN] = useState<number | null>(null);

  if (!id) return <p className="text-sm text-muted-foreground">No job named.</p>;
  if (error) return <p className="text-sm text-destructive">{error.message}</p>;
  if (!job) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const runs = job.runs ?? [];
  const run = runs.find((r) => r.n === n) ?? runs.find((r) => r.state !== "queued") ?? runs[0];

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={job.name || <span className="font-mono">{job.id}</span>}
        description={
          <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
            {job.name ? <span className="font-mono">{job.id}</span> : null}
            <span>submitted {formatRelative(job.created_at)}</span>
            {job.expires_at ? <span>· kept until {formatDateTime(job.expires_at)}</span> : null}
          </span>
        }
        actions={
          job.state === "running" && can("sandbox:create") ? (
            <Button variant="destructive" size="sm" disabled={cancel.isPending}
              onClick={() => confirm("Cancel this job? Its running sandboxes are terminated.") && cancel.mutate(job.id, { onError: (e) => toast.error(e.message), onSuccess: () => toast.success("Cancelled") })}>
              Cancel job
            </Button>
          ) : undefined
        }
      >
        <div className="mt-2 flex flex-wrap items-center gap-3 text-sm">
          <StatusBadge outcome={job.state} />
          <span className="font-mono text-xs text-muted-foreground tabular-nums">
            {job.succeeded} ok · {job.failed} failed · {job.running} running · {job.queued} queued
          </span>
        </div>
        {job.error ? <p className="text-sm text-destructive">{job.error}</p> : null}
      </PageHeader>

      <Spec job={job} />

      <section className="flex flex-col gap-3">
        <SectionHeader title="Runs" />
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Run</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Exit</TableHead>
              <TableHead>Attempts</TableHead>
              <TableHead>Sandbox</TableHead>
              <TableHead>Started</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {runs.map((r) => (
              <TableRow key={r.n} className={cn("cursor-pointer", run?.n === r.n && "bg-muted/50")} onClick={() => setN(r.n)}>
                <TableCell className="font-mono text-xs">#{r.n}</TableCell>
                <TableCell>
                  <StatusBadge outcome={r.state} size="sm" />
                </TableCell>
                <TableCell className="font-mono text-xs tabular-nums">{r.exit_code ?? DASH}</TableCell>
                <TableCell className="font-mono text-xs tabular-nums">{r.attempts}</TableCell>
                <TableCell className="font-mono text-xs text-muted-foreground">
                  {r.sandbox && r.state === "running" ? <Link href={`/sandbox?id=${r.sandbox}`} className="hover:underline">{r.sandbox}</Link> : (r.sandbox ?? DASH)}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">{formatRelative(r.started_at)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </section>

      {run ? <RunKept key={run.n} job={job} run={run} /> : null}
    </div>
  );
}

function Spec({ job }: { job: Job }) {
  const s = job.spec;
  const rows: [string, React.ReactNode][] = [
    s.agent ? ["agent", s.agent] : ["command", formatArgv(s.command ?? [])],
    ["image", s.image || "the gateway's default"],
    ["retries", s.retries ?? 0],
    ["timeout", `${s.timeout_secs ?? 3600} s`],
  ];
  if (s.prompts?.length) rows.push(["prompts", s.prompts.length]);
  if (s.secrets?.length) rows.push(["secrets", s.secrets.join(", ")]);
  if (job.env_names?.length) rows.push(["env", job.env_names.join(", ")]);
  if (s.keep?.files?.length) rows.push(["keeps", s.keep.files.join(", ")]);
  return (
    <dl className="grid max-w-3xl grid-cols-[8rem_1fr] gap-y-1.5 text-sm">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-muted-foreground">{k}</dt>
          <dd className="min-w-0 truncate font-mono text-[13px]">{v}</dd>
        </div>
      ))}
      {s.prompt ? (
        <>
          <dt className="text-muted-foreground">prompt</dt>
          <dd className="whitespace-pre-wrap text-[13px]">{s.prompt}</dd>
        </>
      ) : null}
    </dl>
  );
}

/** What was kept of one run once it ended: stdout, stderr and files. */
function RunKept({ job, run }: { job: Job; run: JobRun }) {
  const ended = !["queued", "running"].includes(run.state);
  const { data, error } = useJobOutput(job.id, run.n, ended && job.spec.keep?.output !== false);
  const stdout = decodeB64(data?.stdout);
  const stderr = decodeB64(data?.stderr);
  return (
    <section className="flex flex-col gap-3">
      <SectionHeader
        title={`Run #${run.n}`}
        description={run.error ? run.error : ended ? `ended ${formatRelative(run.finished_at)}` : "Output is kept when the run ends."}
      />
      {ended && (
        <>
          {error ? <p className="text-sm text-muted-foreground">{error.message}</p> : null}
          {data && (
            <pre className="max-h-[32rem] min-h-24 overflow-auto rounded-md border bg-[#0b0b0c] p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap text-[#e7e7ea]">
              {stdout}
              {stderr ? <span className="text-[#f0a3a3]">{stderr}</span> : null}
              {!stdout && !stderr ? <span className="opacity-60">no output</span> : null}
            </pre>
          )}
          {data?.truncated || run.output_truncated ? <p className="text-xs text-muted-foreground">Output outgrew what is kept; the start is shown.</p> : null}
        </>
      )}
      {run.files?.length ? (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>File</TableHead>
              <TableHead>Size</TableHead>
              <TableHead className="text-right" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {run.files.map((f) => (
              <TableRow key={f.path}>
                <TableCell className="font-mono text-xs">{f.path}</TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {f.error ? f.error : `${formatBytes(f.size)}${f.truncated ? " (truncated)" : ""}`}
                </TableCell>
                <TableCell className="text-right">
                  {!f.error && (
                    <Button size="sm" variant="ghost" onClick={() => downloadJobFile(job.id, run.n, f.path).catch((e: Error) => toast.error(e.message))}>
                      <Download className="size-3.5" />
                      Download
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      ) : null}
    </section>
  );
}

export default function JobPage() {
  return (
    <Gate need="gateway">
      <Suspense fallback={<p className="text-sm text-muted-foreground">Loading…</p>}>
        <JobDetail />
      </Suspense>
    </Gate>
  );
}
