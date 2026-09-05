"use client";

import { useEffect, useState } from "react";
import { History } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { useSetSnapshotSettings, useSnapshotSettings } from "@/lib/api/queries";
import { humanDuration } from "@/lib/format";

/**
 * How long snapshots are kept, by kind.
 *
 * Two windows rather than one, because the two things are not alike: a crash
 * snapshot is insurance against something nobody saw coming and has to still be
 * there when they finally look, while a checkpoint is taken before a known risk
 * by somebody who is right there and will use it within the hour or not at all.
 *
 * What this card writes is a file of Studio's own
 * (~/.config/sandbox/studio/snapshots.json), never the user's config.yaml —
 * rewriting a hand-maintained YAML file loses its comments and its ordering, a
 * cost nobody agreed to when they typed in a box. That makes it a layer *under*
 * config.yaml, so a value set there by hand wins; the field says so and stops
 * offering an edit that would not survive a restart, because a control that
 * silently does nothing is worse than one that is not there.
 */
export function SnapshotsCard() {
  const { data } = useSnapshotSettings();
  const save = useSetSnapshotSettings();
  const [run, setRun] = useState("");
  const [manual, setManual] = useState("");

  // Seeded from the daemon once it answers, and only then: a controlled input
  // starting empty would save "" — which clears the override — for anybody who
  // pressed Save before the fetch landed.
  useEffect(() => {
    if (!data) return;
    setRun(data.configRetention || data.retention);
    setManual(data.configManualRetention || data.manualRetention);
  }, [data]);

  const runPinned = !!data?.configRetention;
  const manualPinned = !!data?.configManualRetention;

  return (
    <Card className="surface-sheen gap-4">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <History className="size-4" />
          Snapshot retention
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <Field
          id="manual-retention"
          label="Snapshots you take"
          help="A checkpoint before a risky step. Seven days by default."
          value={manual}
          onChange={setManual}
          pinned={manualPinned}
          effective={data?.manualRetention}
        />
        <Field
          id="run-retention"
          label="Crash snapshots"
          help="Recorded on a timer while a run is in flight. Fourteen days by default."
          value={run}
          onChange={setRun}
          pinned={runPinned}
          effective={data?.retention}
        />

        {!data?.writable && (
          <p className="text-xs text-caution">
            No config directory could be resolved on the daemon&apos;s machine, so nothing
            can be saved here.
          </p>
        )}

        <div className="flex items-center gap-3">
          <Button
            size="sm"
            disabled={save.isPending || !data?.writable || (runPinned && manualPinned)}
            onClick={() =>
              save.mutate({
                // A pinned field is config.yaml's, so it is not sent back: a UI
                // that echoed the resolved value would copy config.yaml's setting
                // into Studio's file, where it would outlive the line it came
                // from — the mistake the Agents page made with provider hosts.
                retention: runPinned ? "" : run.trim(),
                manualRetention: manualPinned ? "" : manual.trim(),
                writable: true,
              })
            }
          >
            Save
          </Button>
          <p className="text-[11px] text-muted-foreground">
            A Go duration — 72h, 168h, 720h. Empty returns it to the built-in default.
          </p>
        </div>
      </CardContent>
    </Card>
  );
}

function Field({
  id,
  label,
  help,
  value,
  onChange,
  pinned,
  effective,
}: {
  id: string;
  label: string;
  help: string;
  value: string;
  onChange: (v: string) => void;
  pinned: boolean;
  effective?: string;
}) {
  return (
    <div className="space-y-1.5">
      <div className="flex items-center gap-2">
        <Label htmlFor={id}>{label}</Label>
        {pinned ? (
          <Badge variant="outline" className="text-[10px]">
            set in config.yaml
          </Badge>
        ) : (
          <span className="text-[11px] text-muted-foreground tabular-nums">
            now {humanDuration(effective)}
          </span>
        )}
      </div>
      <Input
        id={id}
        value={value}
        disabled={pinned}
        onChange={(e) => onChange(e.target.value)}
        placeholder="168h"
        className="font-mono"
      />
      <p className="text-[11px] text-muted-foreground">
        {pinned
          ? "Typed by hand in config.yaml, which outranks this screen. Edit it there."
          : help}
      </p>
    </div>
  );
}
