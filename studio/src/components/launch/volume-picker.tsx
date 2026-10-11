"use client";

import Link from "next/link";
import { Plus, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useInfo, useVolumes } from "@/lib/api/queries";
import { formatMiB } from "@/lib/format";

export interface Mount {
  name: string;
  path: string;
  readOnly: boolean;
}

/**
 * Volumes to mount, picked from the ones that exist (GET /v1/volumes) rather
 * than typed. A volume is mounted in one live sandbox at a time, so one
 * attached elsewhere is listed but cannot be picked, and one already picked
 * is not offered twice. The path is the caller's to choose; it starts as
 * /mnt/NAME.
 */
export function VolumePicker({ mounts, onChange }: { mounts: Mount[]; onChange: (m: Mount[]) => void }) {
  const { data: info } = useInfo();
  const supported = !!info?.capabilities?.capabilities.volumes;
  const { data: volumes, isLoading } = useVolumes(supported);
  if (info && !supported) return <p className="text-xs text-muted-foreground">This endpoint has no volumes.</p>;

  const list = volumes ?? [];
  const picked = new Set(mounts.map((m) => m.name));
  const free = list.filter((v) => !v.attached_to && !picked.has(v.name));
  const set = (i: number, m: Partial<Mount>) => onChange(mounts.map((x, j) => (j === i ? { ...x, ...m } : x)));

  return (
    <div className="flex flex-col gap-2">
      {mounts.map((m, i) => (
        <div key={i} className="flex flex-wrap items-center gap-2">
          <Select
            value={m.name}
            onValueChange={(name) => set(i, { name, path: !m.path || m.path === `/mnt/${m.name}` ? `/mnt/${name}` : m.path })}
          >
            <SelectTrigger aria-label={`Volume ${i + 1}`} className="h-8 w-44 font-mono text-xs">
              <SelectValue placeholder="volume" />
            </SelectTrigger>
            <SelectContent>
              {list.map((v) => (
                <SelectItem key={v.name} value={v.name} disabled={(!!v.attached_to || picked.has(v.name)) && v.name !== m.name} className="font-mono text-xs">
                  {v.name}
                  <span className="ml-2 text-muted-foreground">
                    {formatMiB(v.size_mb)}
                    {v.attached_to ? ` · in ${v.attached_to}` : ""}
                  </span>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Input aria-label={`Mount path ${i + 1}`} className="h-8 min-w-0 flex-1 font-mono text-xs" placeholder="/mnt/data" value={m.path} onChange={(e) => set(i, { path: e.target.value })} />
          <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <Checkbox checked={m.readOnly} onCheckedChange={(c) => set(i, { readOnly: c === true })} aria-label={`Read-only ${i + 1}`} />
            read-only
          </label>
          <Button type="button" variant="ghost" size="icon" className="size-8" aria-label={`Remove volume ${i + 1}`} onClick={() => onChange(mounts.filter((_, j) => j !== i))}>
            <X className="size-4" />
          </Button>
        </div>
      ))}
      {isLoading ? (
        <p className="text-xs text-muted-foreground">Loading volumes…</p>
      ) : list.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          No volumes yet. <Link href="/volumes" className="underline">Create one in Volumes</Link>.
        </p>
      ) : (
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="w-fit gap-1.5"
          disabled={free.length === 0}
          onClick={() => onChange([...mounts, { name: free[0].name, path: `/mnt/${free[0].name}`, readOnly: false }])}
        >
          <Plus className="size-3.5" /> {free.length === 0 ? "Every volume is picked or in use" : "Add a volume"}
        </Button>
      )}
    </div>
  );
}
