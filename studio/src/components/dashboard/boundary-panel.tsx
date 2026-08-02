"use client";

import Link from "next/link";
import { AlertTriangle, CheckCircle2, CircleHelp, ShieldCheck, XCircle } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useDoctor } from "@/lib/api/queries";
import { egressSummary } from "@/lib/derive";
import { pluralize } from "@/lib/format";
import type { CheckResult, Run } from "@/lib/types";
import { cn } from "@/lib/utils";

const RESULT_ICON: Record<CheckResult, typeof CheckCircle2> = {
  pass: CheckCircle2,
  warn: AlertTriangle,
  fail: XCircle,
  unknown: CircleHelp,
};

const RESULT_TONE: Record<CheckResult, string> = {
  pass: "text-status-good",
  warn: "text-status-warning",
  fail: "text-status-critical",
  unknown: "text-muted-foreground",
};

/**
 * The boundary, in one card: what the host can actually deliver, and what the
 * runs on screen were permitted to reach.
 *
 * `unknown` is drawn as its own thing rather than as a pass, because that is the
 * distinction `doctor` makes — a question that could not be *asked* does not get
 * to assume the answer it would prefer.
 */
export function BoundaryPanel({ runs, loading }: { runs: Run[]; loading?: boolean }) {
  const { data: checks, isPending } = useDoctor();
  const egress = egressSummary(runs);

  const failed = checks?.filter((c) => c.result === "fail") ?? [];
  const warned = checks?.filter((c) => c.result === "warn") ?? [];
  const unknown = checks?.filter((c) => c.result === "unknown") ?? [];
  const worst: CheckResult = failed.length
    ? "fail"
    : unknown.length
      ? "unknown"
      : warned.length
        ? "warn"
        : "pass";
  const WorstIcon = RESULT_ICON[worst];

  return (
    <Card className="surface-sheen gap-3">
      <CardHeader className="gap-1">
        <div className="flex items-center justify-between gap-2">
          <CardTitle className="flex items-center gap-2 text-sm font-medium">
            <ShieldCheck className="size-4 text-muted-foreground" />
            Boundary
          </CardTitle>
          <Button asChild variant="ghost" size="sm" className="h-7 text-xs">
            <Link href="/settings/doctor">Run doctor</Link>
          </Button>
        </div>
      </CardHeader>

      <CardContent className="space-y-4">
        {isPending ? (
          <Skeleton className="h-10 w-full" />
        ) : (
          <div className="flex items-start gap-2.5 rounded-md border bg-card/40 p-2.5">
            <WorstIcon className={cn("mt-0.5 size-4 shrink-0", RESULT_TONE[worst])} />
            <div className="min-w-0 text-xs">
              <p className="font-medium">
                {worst === "pass"
                  ? `All ${checks?.length ?? 0} host checks pass`
                  : failed.length
                    ? `${pluralize(failed.length, "check")} failing`
                    : unknown.length
                      ? `${pluralize(unknown.length, "check")} could not be asked`
                      : `${pluralize(warned.length, "check")} warning`}
              </p>
              <p className="mt-0.5 text-muted-foreground">
                {worst === "pass"
                  ? "Seccomp applied, the container can program iptables, and the workspace is writable by the sandbox user."
                  : (failed[0]?.detail ?? unknown[0]?.detail ?? warned[0]?.detail)}
              </p>
            </div>
          </div>
        )}

        <div className="space-y-2.5">
          <p className="text-[11px] font-medium tracking-wide text-muted-foreground uppercase">
            Egress across these runs
          </p>
          {loading ? (
            <Skeleton className="h-16 w-full" />
          ) : (
            <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-xs">
              <Row
                label="Allowlisted"
                value={String(egress.byMode.allowlist)}
                tone="text-contained"
                hint="A default-deny firewall, programmed as root before the privilege drop. A run that asks for one and cannot program it refuses to start rather than running open."
              />
              <Row
                label="No network"
                value={String(egress.byMode.none)}
                tone="text-contained"
                hint="`--network none`. The way to ask to reach nothing."
              />
              <Row
                label="Unrestricted"
                value={String(egress.byMode.default)}
                tone={egress.byMode.default > 0 ? "text-exposed" : "text-muted-foreground"}
                hint="Open egress. Any credential the agent holds can leave."
              />
              <Row
                label="Distinct domains"
                value={String(egress.distinctDomains)}
                tone="text-foreground"
                hint="The union of every resolved allowlist. The domains come from several merged layers, which is why each run records its own."
              />
              <Row
                label="Decided by name"
                value={String(egress.byName)}
                tone="text-contained"
                hint="The in-container proxy read the hostname from the TLS SNI, a CONNECT, or a Host header and resolved fresh — so a host sharing an allowlisted address does not ride in on it."
              />
              <Row
                label="Matched on address"
                value={String(egress.byAddress)}
                tone={egress.byAddress > 0 ? "text-caution" : "text-muted-foreground"}
                hint="Address matching only. A host sharing an allowlisted IP is reachable, and names resolved once at start can rotate away mid-session."
              />
            </dl>
          )}
        </div>
      </CardContent>
    </Card>
  );
}

function Row({
  label,
  value,
  tone,
  hint,
}: {
  label: string;
  value: string;
  tone: string;
  hint: string;
}) {
  return (
    <div className="flex items-baseline justify-between gap-2">
      <Tooltip>
        <TooltipTrigger asChild>
          <dt className="cursor-help truncate text-muted-foreground underline decoration-dotted decoration-muted-foreground/40 underline-offset-2">
            {label}
          </dt>
        </TooltipTrigger>
        <TooltipContent className="max-w-xs">{hint}</TooltipContent>
      </Tooltip>
      <dd className={cn("font-mono font-medium tabular-nums", tone)}>{value}</dd>
    </div>
  );
}
