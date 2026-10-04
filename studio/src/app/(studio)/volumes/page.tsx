"use client";

import { useState } from "react";
import Link from "next/link";
import { HardDrive } from "lucide-react";
import { toast } from "sonner";
import { PageHeader } from "@/components/common/page-header";
import { EmptyState } from "@/components/common/empty-state";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useCreateVolume, useDeleteVolume, useInfo, useVolumes } from "@/lib/api/queries";
import { formatRelative } from "@/lib/format";
import { useCan } from "@/lib/caller";

/**
 * An agent's tools volume, which sandbox-cli makes on the first run of an agent
 * the image does not carry: agent-<name>-<8 hex>, the hash of its install.
 */
const TOOLS_VOLUME = /^agent-([a-z0-9-]+)-[0-9a-f]{8}$/;

/** Named filesystems that outlive the sandboxes they are mounted in: one writer, or any number of readers. */
export default function VolumesPage() {
  const { data: info } = useInfo();
  const supported = info?.capabilities?.capabilities.volumes;
  const { data, error } = useVolumes(!!supported);
  const create = useCreateVolume();
  const del = useDeleteVolume();
  const can = useCan();
  const [name, setName] = useState("");
  const [size, setSize] = useState("");

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Volumes"
        description="Mount one at launch (Sandbox options → Volumes). A volume has one writer or any number of read-only readers, and the host never mounts it. agent-… volumes hold an agent the image lacks, installed once and mounted read-only by its runs."
      />
      {info && !supported ? (
        <EmptyState icon={HardDrive} title="This sandboxd has no volumes" description="Its backend does not offer them (capability volumes)." />
      ) : (
        <>
          {can("sandbox:create") && <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              create.mutate(
                { name, size_mb: size ? Number(size) : undefined },
                { onSuccess: () => { setName(""); setSize(""); }, onError: (err) => toast.error(err.message) },
              );
            }}
          >
            <Input placeholder="name" value={name} onChange={(e) => setName(e.target.value)} className="w-48 font-mono" required />
            <Input placeholder="size MiB (default: server's)" value={size} onChange={(e) => setSize(e.target.value.replace(/\D/g, ""))} className="w-56" />
            <Button type="submit" disabled={create.isPending}>Create</Button>
          </form>}
          {error ? <p className="text-sm text-destructive">{error.message}</p> : null}
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Volume</TableHead>
                <TableHead>Size</TableHead>
                <TableHead>Created</TableHead>
                <TableHead>Mounted in</TableHead>
                <TableHead className="text-right" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {(data ?? []).map((v) => (
                <TableRow key={v.name}>
                  <TableCell className="font-mono text-sm">
                    {v.name}
                    {TOOLS_VOLUME.test(v.name) && (
                      <span className="ml-2 font-sans text-xs text-muted-foreground">
                        {TOOLS_VOLUME.exec(v.name)?.[1]}&apos;s tools · read-only in runs
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="text-sm">{v.size_mb} MiB</TableCell>
                  <TableCell className="text-xs text-muted-foreground">{formatRelative(v.created_at)}</TableCell>
                  <TableCell className="font-mono text-xs">
                    {v.attached_to ? <Link href={`/sandbox?id=${v.attached_to}`} className="hover:underline">{v.attached_to}</Link> : "—"}
                  </TableCell>
                  <TableCell className="text-right">
                    {can("sandbox:delete") && <Button size="sm" variant="ghost" disabled={!!v.attached_to || del.isPending}
                      onClick={() => confirm(`Delete ${v.name} and everything on it?`) && del.mutate(v.name, { onError: (e) => toast.error(e.message) })}>
                      Delete
                    </Button>}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </>
      )}
    </div>
  );
}
