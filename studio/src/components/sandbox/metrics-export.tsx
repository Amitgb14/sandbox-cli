"use client";

import { useState } from "react";
import { toast } from "sonner";
import { Plus, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useSecrets } from "@/lib/api/gateway";
import { useInfo, useUpdateSandbox } from "@/lib/api/queries";
import { isGateway, useCaller } from "@/lib/caller";
import { formatDateTime, formatRelative } from "@/lib/format";
import type { MetricsExport, MetricsExportStatus, Sandbox } from "@/lib/types";

/**
 * A sandbox's metrics export: its CPU, memory, network and disk, as the host
 * measures them, pushed to the user's own OTLP/HTTP collector. The endpoint
 * and how often are shown back; a header's value is not — it goes to the
 * server and never returns, so changing the export means entering the
 * headers again. Through a gateway a header can name one of the user's
 * secrets instead, and the value never reaches the browser at all.
 */

/** Intervals offered, in seconds; the server's minimum trims the list. */
const INTERVALS = [10, 15, 30, 60, 120, 300, 600];

export function intervalLabel(secs: number): string {
  return secs % 60 === 0 ? `${secs / 60} min` : `${secs} s`;
}

export interface HeaderRow {
  name: string;
  /** The value typed, or the secret's name where fromSecret. */
  value: string;
  fromSecret: boolean;
}

export interface ExportForm {
  endpoint: string;
  intervalSecs: number;
  headers: HeaderRow[];
}

export const emptyExport: ExportForm = { endpoint: "", intervalSecs: 60, headers: [] };

const HEADER = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/;

/** What is wrong with the form, or "". The server checks it all again. */
export function exportProblem(f: ExportForm): string {
  if (!f.endpoint.trim()) return f.headers.length ? "Headers need an endpoint" : "";
  let u: URL;
  try {
    u = new URL(f.endpoint.trim());
  } catch {
    return "The endpoint is not a URL";
  }
  if (u.protocol !== "https:" && u.protocol !== "http:") return "The endpoint is an https URL";
  if (u.username || u.password || u.search) return "Put a credential in a header, not in the URL";
  for (const h of f.headers) {
    if (!HEADER.test(h.name.trim())) return `${h.name.trim() || "A header"}: not a header name`;
    if (!h.value) return `${h.name.trim()}: no value`;
  }
  const names = f.headers.map((h) => h.name.trim().toLowerCase());
  const dup = names.find((n, i) => names.indexOf(n) !== i);
  return dup ? `${dup} given twice` : "";
}

/** The API's export from the form; undefined when no endpoint is given. */
export function toExport(f: ExportForm): MetricsExport | undefined {
  const endpoint = f.endpoint.trim();
  if (!endpoint) return undefined;
  const me: MetricsExport = { endpoint, interval_secs: f.intervalSecs };
  for (const h of f.headers) {
    const name = h.name.trim();
    if (h.fromSecret) me.header_secrets = { ...me.header_secrets, [name]: h.value };
    else me.headers = { ...me.headers, [name]: h.value };
  }
  return me;
}

/** The endpoint, interval and headers, for the Playground and the edit dialog. */
export function ExportFields({ value, onChange, idPrefix = "export" }: { value: ExportForm; onChange: (f: ExportForm) => void; idPrefix?: string }) {
  const { data: info } = useInfo();
  const caller = useCaller();
  const gateway = isGateway(caller);
  // Secrets are a gateway's; a plain sandboxd has none to list.
  const { data: secrets } = useSecrets(gateway);
  const min = info?.capabilities?.limits.min_metrics_export_secs ?? 10;
  const intervals = [...new Set([...INTERVALS.filter((s) => s >= min), Math.max(min, value.intervalSecs)])].sort((a, b) => a - b);
  const set = (patch: Partial<ExportForm>) => onChange({ ...value, ...patch });
  const setRow = (i: number, patch: Partial<HeaderRow>) => set({ headers: value.headers.map((h, j) => (j === i ? { ...h, ...patch } : h)) });
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-col gap-2">
        <Label htmlFor={`${idPrefix}-endpoint`}>OTLP endpoint</Label>
        <Input
          id={`${idPrefix}-endpoint`}
          className="font-mono"
          placeholder="https://otlp.example.com"
          value={value.endpoint}
          onChange={(e) => set({ endpoint: e.target.value })}
        />
        <span className="text-[11px] text-muted-foreground">
          Your collector, over OTLP/HTTP. Without a path, /v1/metrics. Pushed from the host: https to a public address, unless the operator allows private ones.
        </span>
      </div>
      <div className="flex flex-col gap-2">
        <Label>Every</Label>
        <Select value={String(value.intervalSecs)} onValueChange={(v) => set({ intervalSecs: Number(v) })}>
          <SelectTrigger className="w-32" aria-label="Push every">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {intervals.map((s) => (
              <SelectItem key={s} value={String(s)}>
                {intervalLabel(s)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <div className="flex flex-col gap-2">
        <Label>Headers</Label>
        {value.headers.map((h, i) => (
          <div key={i} className="flex items-center gap-2">
            <Input aria-label="Header name" className="w-40 font-mono" placeholder="Authorization" value={h.name} onChange={(e) => setRow(i, { name: e.target.value })} />
            {h.fromSecret ? (
              <Select value={h.value} onValueChange={(v) => setRow(i, { value: v })}>
                <SelectTrigger className="min-w-0 flex-1" aria-label="Secret">
                  <SelectValue placeholder="a secret" />
                </SelectTrigger>
                <SelectContent>
                  {(secrets ?? []).map((s) => (
                    <SelectItem key={s.name} value={s.name}>
                      {s.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : (
              <Input
                aria-label="Header value"
                type="password"
                autoComplete="off"
                className="min-w-0 flex-1 font-mono"
                placeholder="Bearer …"
                value={h.value}
                onChange={(e) => setRow(i, { value: e.target.value })}
              />
            )}
            {gateway ? (
              <Button type="button" variant="ghost" size="sm" className="shrink-0 text-xs" onClick={() => setRow(i, { fromSecret: !h.fromSecret, value: "" })}>
                {h.fromSecret ? "Type it" : "From a secret"}
              </Button>
            ) : null}
            <Button type="button" variant="ghost" size="icon" aria-label="Remove header" onClick={() => set({ headers: value.headers.filter((_, j) => j !== i) })}>
              <X className="size-4" />
            </Button>
          </div>
        ))}
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="w-fit"
          onClick={() => set({ headers: [...value.headers, { name: value.headers.length ? "" : "Authorization", value: "", fromSecret: false }] })}
        >
          <Plus className="size-4" /> Add header
        </Button>
        <span className="text-[11px] text-muted-foreground">
          For your collector&apos;s credential. Sent with every push and never shown again{gateway ? "; a secret's value never reaches this browser" : ""}.
        </span>
      </div>
    </div>
  );
}

/** How the export is going, in a line. */
export function ExportStatus({ st }: { st: MetricsExportStatus }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <span className="truncate font-mono text-[13px]" title={st.endpoint}>
        {st.endpoint}
      </span>
      <span className="text-xs text-muted-foreground">
        every {intervalLabel(st.interval_secs)}
        {st.header_names?.length ? ` · headers ${st.header_names.join(", ")}` : ""}
      </span>
      {st.last_error ? (
        <span className="text-xs text-destructive" title={st.failing_since ? `failing since ${formatDateTime(st.failing_since)}` : undefined}>
          Failing{st.failing_since ? ` since ${formatRelative(st.failing_since)}` : ""}: {st.last_error}
        </span>
      ) : st.last_push_at ? (
        <span className="text-xs text-muted-foreground" title={formatDateTime(st.last_push_at)}>
          Last push {formatRelative(st.last_push_at)}
        </span>
      ) : (
        <span className="text-xs text-muted-foreground">Waiting for the first push</span>
      )}
    </div>
  );
}

/** Sets, replaces or stops a live sandbox's export (PATCH /v1/sandboxes/{ref}). */
export function EditMetricsExport({ sb, open, onOpenChange }: { sb: Sandbox; open: boolean; onOpenChange: (o: boolean) => void }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">{open ? <EditForm sb={sb} onDone={() => onOpenChange(false)} /> : null}</DialogContent>
    </Dialog>
  );
}

function EditForm({ sb, onDone }: { sb: Sandbox; onDone: () => void }) {
  const update = useUpdateSandbox();
  const cur = sb.metrics_export;
  // The header names come back, their values do not: each is entered again.
  const [form, setForm] = useState<ExportForm>(
    cur
      ? { endpoint: cur.endpoint, intervalSecs: cur.interval_secs, headers: (cur.header_names ?? []).map((name) => ({ name, value: "", fromSecret: false })) }
      : emptyExport,
  );
  const problem = exportProblem(form);
  function save(me: MetricsExport) {
    update.mutate(
      { id: sb.id, metrics_export: me },
      {
        onSuccess: () => {
          toast.success(me.endpoint ? `Exporting ${sb.name || sb.id}'s metrics` : `Stopped exporting ${sb.name || sb.id}'s metrics`);
          onDone();
        },
        onError: (err) => toast.error(err.message),
      },
    );
  }
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        const me = toExport(form);
        if (problem || !me) return;
        save(me);
      }}
      className="flex flex-col gap-4"
    >
      <DialogHeader>
        <DialogTitle>Metrics export</DialogTitle>
        <DialogDescription>
          Push this sandbox&apos;s CPU, memory, network and disk to your OpenTelemetry collector.
          {cur?.header_names?.length ? " Header values are never shown: enter them again, or they are dropped." : ""}
        </DialogDescription>
      </DialogHeader>
      <ExportFields value={form} onChange={setForm} idPrefix="edit-export" />
      {problem && form.endpoint ? <p className="text-xs text-destructive">{problem}</p> : null}
      <DialogFooter className="gap-2">
        {cur ? (
          <Button type="button" variant="outline" disabled={update.isPending} onClick={() => save({ endpoint: "" })}>
            Stop exporting
          </Button>
        ) : null}
        <Button type="submit" disabled={update.isPending || !!problem || !form.endpoint.trim()}>
          Save
        </Button>
      </DialogFooter>
    </form>
  );
}
