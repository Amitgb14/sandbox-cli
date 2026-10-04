"use client";

import { useState } from "react";
import { ScrollText } from "lucide-react";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { Gate } from "@/components/shell/gate";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ApiError } from "@/lib/api/client";
import { useAudit } from "@/lib/admin/api";
import { DASH, formatDateTime } from "@/lib/format";
import { cn } from "@/lib/utils";

const WINDOWS: [string, string, number][] = [
  ["15m", "Last 15 minutes", 15 * 60_000],
  ["1h", "Last hour", 3_600_000],
  ["24h", "Last day", 86_400_000],
  ["7d", "Last week", 7 * 86_400_000],
];

/**
 * The gateway's audit record: every authenticated request and every SSH
 * login and session, naming a key by its id and an SSH key by its
 * fingerprint — never a secret. Newest first here; the API sends oldest
 * first, at most 1000.
 */
function Audit() {
  const [win, setWin] = useState("1h");
  // The window's start, fixed when it is chosen so the query key is stable.
  const [since, setSince] = useState(() => new Date(Date.now() - 3_600_000).toISOString());
  const { data, isLoading, error } = useAudit(since);
  const entries = [...(data?.entries ?? [])].reverse();

  return (
    <div className="flex flex-col gap-5">
      <PageHeader title="Audit" description="Every authenticated API request and SSH login the gateway recorded. Keys are named by id, SSH keys by fingerprint." />
      <div className="flex items-center gap-2">
        <Select
          value={win}
          onValueChange={(v) => {
            setWin(v);
            setSince(new Date(Date.now() - WINDOWS.find((w) => w[0] === v)![2]).toISOString());
          }}
        >
          <SelectTrigger size="sm" className="w-44" aria-label="Window">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {WINDOWS.map(([id, label]) => (
              <SelectItem key={id} value={id}>
                {label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {data?.truncated ? <span className="text-xs text-muted-foreground">The oldest 1000 in the window; choose a shorter one for the latest.</span> : null}
      </div>
      {error instanceof ApiError && error.status === 501 ? (
        <EmptyState icon={ScrollText} title="This gateway keeps no audit log" description={error.message} />
      ) : error ? (
        <p className="text-sm text-destructive">{error.message}</p>
      ) : !isLoading && entries.length === 0 ? (
        <EmptyState icon={ScrollText} title="Nothing recorded in this window" />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Time</TableHead>
              <TableHead>Action</TableHead>
              <TableHead>Who</TableHead>
              <TableHead>On</TableHead>
              <TableHead>Result</TableHead>
              <TableHead>From</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {entries.map((e, i) => {
              const bad = (e.status ?? 0) >= 400 || e.result === "refused";
              return (
                <TableRow key={`${e.time}-${i}`}>
                  <TableCell className="text-xs whitespace-nowrap text-muted-foreground" title={e.time}>{formatDateTime(e.time)}</TableCell>
                  <TableCell className="font-mono text-xs">
                    {e.action}
                    {e.session ? <span className="text-muted-foreground"> · {e.session}</span> : null}
                  </TableCell>
                  <TableCell className="text-xs">
                    {e.user ?? DASH}
                    {e.tenant ? <span className="text-muted-foreground">@{e.tenant}</span> : null}
                    <div className="font-mono text-[11px] text-muted-foreground">{e.key_id ?? e.fingerprint ?? ""}</div>
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">{e.sandbox ?? e.target ?? DASH}{e.node ? ` · ${e.node}` : ""}</TableCell>
                  <TableCell className={cn("font-mono text-xs tabular-nums", bad && "text-destructive")}>{e.status ?? e.result ?? DASH}</TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">{e.remote ?? DASH}</TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      )}
    </div>
  );
}

export default function AuditPage() {
  return (
    <Gate need="admin">
      <Audit />
    </Gate>
  );
}
