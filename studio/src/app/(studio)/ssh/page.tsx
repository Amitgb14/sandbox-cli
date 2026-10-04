"use client";

import { useState } from "react";
import { TerminalSquare, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { CodeBlock } from "@/components/common/code-block";
import { CopyButton } from "@/components/common/copy-button";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader, SectionHeader } from "@/components/common/page-header";
import { Gate } from "@/components/shell/gate";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { useAddSSHKey, useRemoveSSHKey, useSSHAccess, useSSHInfo, useSSHKeys } from "@/lib/api/gateway";
import { useSandboxes } from "@/lib/api/queries";
import { useCan } from "@/lib/caller";
import { formatDateTime, formatRelative } from "@/lib/format";
import type { SSHAccess, SSHInfo } from "@/lib/types";

const TTLS: [string, string][] = [
  ["300", "5 minutes"],
  ["900", "15 minutes"],
  ["3600", "1 hour"],
  ["28800", "8 hours"],
  ["86400", "24 hours"],
];

/**
 * SSH through the gateway: one port for every sandbox, the sandbox as the
 * user name. A login is a registered public key, or a short-lived access
 * token as the user name. Everything but the connection details needs
 * sandbox:ssh, and is not offered without it.
 */
function SSH() {
  const can = useCan();
  const ssh = can("sandbox:ssh");
  const { data: info, isLoading, error } = useSSHInfo();

  return (
    <div className="flex flex-col gap-8">
      <PageHeader title="SSH" description="Every sandbox you own answers on the gateway's SSH port, with the sandbox's name or id as the user name. sandbox-cli ssh registers your key and pins the host key for you." />
      {error ? (
        <p className="text-sm text-destructive">{error.message}</p>
      ) : isLoading ? null : !info ? (
        <EmptyState icon={TerminalSquare} title="This gateway serves no SSH" description="Its operator has not started it with --ssh-listen." />
      ) : (
        <>
          <Connect info={info} />
          {ssh ? (
            <>
              <Keys />
              <Access />
            </>
          ) : (
            <p className="text-sm text-muted-foreground">This key has no sandbox:ssh scope: it cannot register SSH keys, issue access tokens or log in.</p>
          )}
        </>
      )}
    </div>
  );
}

function Connect({ info }: { info: SSHInfo }) {
  const port = info.port === 22 ? "" : ` -p ${info.port}`;
  const known = info.host_keys.map((k) => `${info.port === 22 ? info.host : `[${info.host}]:${info.port}`} ${k}`).join("\n");
  return (
    <section className="flex flex-col gap-3">
      <SectionHeader title="Connect" description={`${info.host}, port ${info.port}. Host key ${info.fingerprint}.`} />
      <div className="grid max-w-3xl gap-3">
        <CodeBlock title="sandbox-cli" value="sandbox-cli ssh SANDBOX">sandbox-cli ssh SANDBOX</CodeBlock>
        <CodeBlock title="OpenSSH" value={`ssh${port} SANDBOX@${info.host}`}>{`ssh${port} SANDBOX@${info.host}`}</CodeBlock>
        <CodeBlock title="known_hosts" value={known}>{known}</CodeBlock>
      </div>
    </section>
  );
}

function Keys() {
  const { data, error } = useSSHKeys(true);
  const add = useAddSSHKey();
  const remove = useRemoveSSHKey();
  const [key, setKey] = useState("");
  const [sandbox, setSandbox] = useState("");
  return (
    <section className="flex flex-col gap-3">
      <SectionHeader title="Your SSH keys" description="Public keys that log in to your sandboxes; one limited to a sandbox logs in to that one only." />
      <form
        className="flex max-w-3xl flex-col gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          add.mutate(
            { key: key.trim(), sandbox: sandbox.trim() || undefined },
            {
              onSuccess: (k) => {
                toast.success(`Added ${k.fingerprint}`);
                setKey("");
                setSandbox("");
              },
              onError: (err) => toast.error(err.message),
            },
          );
        }}
      >
        <Textarea aria-label="Public key" placeholder="ssh-ed25519 AAAA… you@laptop" value={key} onChange={(e) => setKey(e.target.value)} className="min-h-16 font-mono text-xs" required />
        <div className="flex flex-wrap gap-2">
          <Input aria-label="Only for sandbox" placeholder="only for sandbox (optional)" value={sandbox} onChange={(e) => setSandbox(e.target.value)} className="w-64 font-mono" />
          <Button type="submit" disabled={add.isPending}>Add key</Button>
        </div>
      </form>
      {error ? <p className="text-sm text-destructive">{error.message}</p> : null}
      {(data ?? []).length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Fingerprint</TableHead>
              <TableHead>Key</TableHead>
              <TableHead>Sandbox</TableHead>
              <TableHead>Added</TableHead>
              <TableHead className="text-right" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {data!.map((k) => (
              <TableRow key={k.id}>
                <TableCell className="font-mono text-xs">{k.fingerprint}</TableCell>
                <TableCell className="max-w-64 truncate font-mono text-xs text-muted-foreground" title={k.key}>{k.key}</TableCell>
                <TableCell className="font-mono text-xs">{k.sandbox || "any of yours"}</TableCell>
                <TableCell className="text-xs text-muted-foreground">{formatRelative(k.created)}</TableCell>
                <TableCell className="text-right">
                  <Button size="sm" variant="ghost" disabled={remove.isPending}
                    onClick={() => confirm(`Remove ${k.fingerprint}? It no longer logs in.`) && remove.mutate(k.id, { onError: (e) => toast.error(e.message) })}>
                    Remove
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  );
}

/** A one-off login for one sandbox, shown once: the token is the whole credential until it expires. */
function Access() {
  const { data: sandboxes } = useSandboxes();
  const access = useSSHAccess();
  const [sandbox, setSandbox] = useState("");
  const [ttl, setTtl] = useState("900");
  const [issued, setIssued] = useState<SSHAccess | null>(null);
  const live = (sandboxes ?? []).filter((s) => s.state === "running");
  return (
    <section className="flex flex-col gap-3">
      <SectionHeader title="Access token" description="A short-lived login for one sandbox, for a machine without your key. Anyone holding it can log in until it expires." />
      {live.length === 0 ? (
        <p className="text-sm text-muted-foreground">No running sandbox to issue one for.</p>
      ) : (
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            access.mutate(
              { sandbox, ttl_secs: Number(ttl) },
              { onSuccess: setIssued, onError: (err) => toast.error(err.message) },
            );
          }}
        >
          <Select value={sandbox} onValueChange={setSandbox}>
            <SelectTrigger className="w-64" aria-label="Sandbox">
              <SelectValue placeholder="Sandbox" />
            </SelectTrigger>
            <SelectContent>
              {live.map((s) => (
                <SelectItem key={s.id} value={s.id}>
                  {s.name || s.id}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select value={ttl} onValueChange={setTtl}>
            <SelectTrigger className="w-40" aria-label="Valid for">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {TTLS.map(([v, l]) => (
                <SelectItem key={v} value={v}>
                  {l}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button type="submit" disabled={!sandbox || access.isPending}>Issue token</Button>
        </form>
      )}
      {issued && (
        <div className="flex max-w-3xl flex-col gap-2 rounded-lg border border-caution/40 bg-caution/10 p-4">
          <p className="flex items-center gap-2 text-sm font-medium">
            <TriangleAlert className="size-4" aria-hidden /> Shown once. It logs in to this sandbox until {formatDateTime(issued.expires_at)}.
          </p>
          <div className="flex items-center gap-2">
            <code className="min-w-0 flex-1 truncate rounded bg-background px-2 py-1 font-mono text-xs">{issued.command}</code>
            <CopyButton value={issued.command} label="Copy command" size="sm" />
            <Button size="sm" variant="ghost" onClick={() => setIssued(null)}>Done</Button>
          </div>
        </div>
      )}
    </section>
  );
}

export default function SSHPage() {
  return (
    <Gate need="gateway">
      <SSH />
    </Gate>
  );
}
