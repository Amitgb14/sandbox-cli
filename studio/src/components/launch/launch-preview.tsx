"use client";

import { AlertTriangle, FolderTree, Info, ShieldX } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ArgvBlock } from "@/components/common/code-block";
import { tildify } from "@/lib/format";
import type { LaunchPreview as Preview } from "@/lib/types";

/**
 * What this configuration would actually do.
 *
 * The three groups are ordered by what they cost you. **Refusals** block the
 * launch, and each one carries its reason — a form that only said "invalid" would
 * teach nobody why the boundary is drawn where it is. **Warnings** do not block:
 * they are the deliberate widenings, and the whole point of `--share` is that you
 * asked. **Host paths in reach** is the blast radius, listed rather than counted.
 */
export function LaunchPreview({ preview }: { preview: Preview }) {
  return (
    <div className="space-y-4">
      {preview.refusals.length > 0 && (
        <Card className="surface-sheen gap-3 border-destructive/40 bg-destructive/5">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-sm font-medium text-destructive">
              <ShieldX className="size-4" />
              {preview.refusals.length === 1
                ? "This will be refused"
                : `${preview.refusals.length} refusals`}
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-2.5">
            {preview.refusals.map((r) => (
              <p key={r} className="text-xs leading-relaxed text-muted-foreground">
                {r}
              </p>
            ))}
          </CardContent>
        </Card>
      )}

      {preview.warnings.length > 0 && (
        <Card className="surface-sheen gap-3 border-caution/30 bg-caution/5">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-sm font-medium text-caution">
              <AlertTriangle className="size-4" />
              Worth knowing
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-2.5">
            {preview.warnings.map((w) => (
              <p key={w} className="text-xs leading-relaxed text-muted-foreground">
                {w}
              </p>
            ))}
          </CardContent>
        </Card>
      )}

      <Card className="surface-sheen gap-3">
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-sm font-medium">
            <FolderTree className="size-4 text-muted-foreground" />
            Host paths in reach
            <span className="text-muted-foreground tabular-nums">
              {preview.hostPathsInReach.length}
            </span>
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-1.5">
          {preview.hostPathsInReach.map((p) => (
            <p key={p} className="truncate font-mono text-xs" title={p}>
              {tildify(p)}
            </p>
          ))}
          <p className="flex items-start gap-1.5 pt-1 text-xs text-muted-foreground">
            <Info className="mt-0.5 size-3.5 shrink-0" />
            Nothing else is a path at all. The host home is never mounted, and{" "}
            <code className="font-mono">HOME</code> inside the container is a fake ephemeral path.
          </p>
        </CardContent>
      </Card>

      <ArgvBlock
        argv={preview.argv}
        title="docker argv (preview)"
        highlight={(flag, value) => {
          if (flag === "-v" && value && !value.includes(":/workspace")) return "widen";
          if (flag === "--cap-add") return "widen";
          if (flag === "--network" && value === "none") return "tighten";
          if (flag === "--cap-drop" || flag === "--security-opt" || flag === "--pids-limit")
            return "tighten";
          return null;
        }}
      />
    </div>
  );
}
