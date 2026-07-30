"use client";

import { useState } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  CircleHelp,
  RotateCw,
  XCircle,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { PageHeader } from "@/components/common/page-header";
import { useDoctor } from "@/lib/api/queries";
import type { CheckResult, DoctorCheck, Profile } from "@/lib/types";
import { cn } from "@/lib/utils";

const ICON: Record<CheckResult, typeof CheckCircle2> = {
  pass: CheckCircle2,
  warn: AlertTriangle,
  fail: XCircle,
  unknown: CircleHelp,
};

const TONE: Record<CheckResult, string> = {
  pass: "text-status-good",
  warn: "text-status-warning",
  fail: "text-status-critical",
  unknown: "text-muted-foreground",
};

/**
 * Doctor — the profile's preflight.
 *
 * The question it asks is not "is this host healthy" but **"can this host
 * deliver what the profile promises"**, which is why the same result reads
 * differently under each: dev warns, prod fails. The profile switch here is that
 * asymmetry made visible rather than two separate reports.
 *
 * A check that could not be *asked* is `unknown`, and under prod that counts as a
 * failure — it does not get to assume the answer it would prefer.
 */
export default function DoctorPage() {
  const { data, isPending, isFetching, refetch } = useDoctor();
  const [profile, setProfile] = useState<Profile>("dev");

  const checks = data ?? [];
  const verdicts = checks.map((c) => ({ check: c, verdict: verdictUnder(c, profile) }));
  const failing = verdicts.filter((v) => v.verdict === "fail");
  const warning = verdicts.filter((v) => v.verdict === "warn");

  return (
    <div className="space-y-6">
      <PageHeader
        title="Doctor"
        description="Whether this host can deliver what the profile promises — seccomp actually applied, a container able to program iptables, a workspace the sandbox user can write. Tried, not queried, because a rootless or userns-remapped daemon cannot answer by inspection."
        actions={
          <Button variant="outline" size="sm" onClick={() => refetch()} disabled={isFetching}>
            <RotateCw className={cn("size-4", isFetching && "animate-spin")} />
            Re-run
          </Button>
        }
      />

      <div className="flex flex-wrap items-center gap-3">
        <Tabs value={profile} onValueChange={(v) => setProfile(v as Profile)}>
          <TabsList>
            <TabsTrigger value="dev">Under dev</TabsTrigger>
            <TabsTrigger value="prod">Under prod</TabsTrigger>
          </TabsList>
        </Tabs>

        {!isPending && (
          <div className="flex items-center gap-2 text-xs">
            {failing.length === 0 ? (
              <Badge
                variant="outline"
                className="gap-1 border-contained/40 bg-contained/10 text-contained"
              >
                <CheckCircle2 className="size-3" />
                would pass · exit 0
              </Badge>
            ) : (
              <Badge
                variant="outline"
                className="gap-1 border-destructive/40 bg-destructive/10 text-destructive"
              >
                <XCircle className="size-3" />
                {failing.length} failing · non-zero exit
              </Badge>
            )}
            {warning.length > 0 && (
              <span className="text-muted-foreground">{warning.length} warning</span>
            )}
          </div>
        )}
      </div>

      {isPending ? (
        <div className="space-y-2">
          {Array.from({ length: 8 }).map((_, i) => (
            <Skeleton key={i} className="h-16 w-full" />
          ))}
        </div>
      ) : (
        <div className="space-y-2">
          {verdicts.map(({ check, verdict }) => {
            const Icon = ICON[check.result];
            return (
              <Card key={check.id} className="surface-sheen gap-0 py-3">
                <CardContent className="flex items-start gap-3 px-4">
                  <Icon className={cn("mt-0.5 size-4 shrink-0", TONE[check.result])} />
                  <div className="min-w-0 flex-1 space-y-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <p className="text-sm font-medium">{check.title}</p>
                      <Badge variant="outline" className="font-mono text-[10px]">
                        {check.id}
                      </Badge>
                      {/* What this result *costs* under the selected profile,
                          which is the whole reason the check exists. */}
                      {verdict === "fail" ? (
                        <Badge
                          variant="outline"
                          className="border-destructive/40 text-[10px] text-destructive"
                        >
                          refuses the run
                        </Badge>
                      ) : verdict === "warn" ? (
                        <Badge variant="outline" className="border-caution/40 text-[10px] text-caution">
                          warns
                        </Badge>
                      ) : null}
                    </div>
                    <p className="text-xs leading-relaxed text-muted-foreground">{check.detail}</p>
                    {check.result !== "pass" && check.underDev !== check.underProd && (
                      <p className="text-xs text-muted-foreground/80">
                        This one {check.underDev === "fail" ? "fails" : "warns"} under dev and{" "}
                        {check.underProd === "fail" ? "fails" : "warns"} under prod.
                      </p>
                    )}
                  </div>
                </CardContent>
              </Card>
            );
          })}
        </div>
      )}

      <div className="rounded-lg border bg-card/40 p-4 text-xs text-muted-foreground">
        <p>
          <span className="font-medium text-foreground">
            The runtime check reports rather than refuses.
          </span>{" "}
          No profile selects a stronger runtime yet, so failing for something the tool does not do
          would be theatre. It is here because it is worth knowing.
        </p>
      </div>
    </div>
  );
}

function verdictUnder(check: DoctorCheck, profile: Profile): "pass" | "warn" | "fail" {
  if (check.result === "pass") return "pass";
  // An unanswerable question is a failure under prod: it does not get to assume
  // the answer it would prefer.
  if (check.result === "unknown") return profile === "prod" ? "fail" : "warn";
  const severity = profile === "prod" ? check.underProd : check.underDev;
  return check.result === "fail" ? severity : severity === "fail" ? "warn" : "warn";
}
