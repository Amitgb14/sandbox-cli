"use client";

import { useState } from "react";
import { TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { CopyButton } from "@/components/common/copy-button";
import { PageHeader, SectionHeader } from "@/components/common/page-header";
import { Gate } from "@/components/shell/gate";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useCreateKey, useKeys, useRemoveUserSSHKey, useRevokeKey, useUserSSHKeys, type CreatedKey } from "@/lib/admin/api";
import { formatRelative } from "@/lib/format";

const SCOPES = ["sandbox:read", "sandbox:create", "sandbox:delete", "sandbox:ssh", "secrets:write", "admin"];
const USER_SCOPES = ["sandbox:read", "sandbox:create", "sandbox:delete", "sandbox:ssh"];

/**
 * Users are their API keys: issue one, revoke one, and see or remove any
 * user's SSH keys. A new key's secret is in one answer only — the gateway
 * stores its hash — so it is shown once, here, and dropped when dismissed.
 */
function Keys() {
  const { data, error } = useKeys();
  const revoke = useRevokeKey();
  const [created, setCreated] = useState<CreatedKey | null>(null);
  const rows = [...(data ?? [])].sort((a, b) => Number(!!a.revoked) - Number(!!b.revoked) || b.created.localeCompare(a.created));

  return (
    <div className="flex flex-col gap-8">
      <PageHeader title="Users & keys" description="A key acts as its user, in its tenant, with the scopes it was issued; two keys for one user see the same sandboxes. Revoking a user's last key also ends their SSH access." />
      {created ? <Secret created={created} onDone={() => setCreated(null)} /> : <CreateKey onCreated={setCreated} />}
      <section className="flex flex-col gap-3">
        <SectionHeader title="API keys" />
        {error ? <p className="text-sm text-destructive">{error.message}</p> : null}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Key</TableHead>
              <TableHead>User</TableHead>
              <TableHead>Scopes</TableHead>
              <TableHead>Issued</TableHead>
              <TableHead className="text-right" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((k) => (
              <TableRow key={k.id} className={k.revoked ? "opacity-50" : ""}>
                <TableCell className="font-mono text-xs">{k.id}</TableCell>
                <TableCell className="text-sm">
                  {k.user}
                  {k.tenant ? <span className="text-muted-foreground">@{k.tenant}</span> : null}
                </TableCell>
                <TableCell>
                  <div className="flex flex-wrap gap-1">
                    {k.scopes.map((s) => (
                      <Badge key={s} variant="outline" className="font-mono text-[11px]">{s}</Badge>
                    ))}
                  </div>
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">{formatRelative(k.created)}</TableCell>
                <TableCell className="text-right">
                  {k.revoked ? (
                    <span className="text-xs text-muted-foreground">revoked</span>
                  ) : (
                    <Button size="sm" variant="ghost" disabled={revoke.isPending}
                      onClick={() => confirm(`Revoke ${k.id} (${k.user})? Every call with it is refused from now on.`) && revoke.mutate(k.id, { onError: (e) => toast.error(e.message), onSuccess: () => toast.success("Revoked") })}>
                      Revoke
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </section>
      <UserSSHKeys />
    </div>
  );
}

function CreateKey({ onCreated }: { onCreated: (k: CreatedKey) => void }) {
  const create = useCreateKey();
  const [user, setUser] = useState("");
  const [tenant, setTenant] = useState("");
  const [scopes, setScopes] = useState<string[]>(USER_SCOPES);
  return (
    <Card className="surface-sheen gap-0 py-0">
      <CardContent className="p-4">
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate(
              { user, tenant: tenant || undefined, scopes },
              {
                onSuccess: (k) => {
                  onCreated(k);
                  setUser("");
                  setTenant("");
                },
                onError: (err) => toast.error(err.message),
              },
            );
          }}
        >
          <p className="text-sm font-medium">Issue a key</p>
          <div className="flex flex-wrap gap-2">
            <Input aria-label="User" placeholder="user" value={user} onChange={(e) => setUser(e.target.value)} className="w-48 font-mono" required />
            <Input aria-label="Tenant" placeholder="tenant (optional)" value={tenant} onChange={(e) => setTenant(e.target.value)} className="w-48 font-mono" />
          </div>
          <div className="flex flex-wrap gap-x-5 gap-y-2">
            {SCOPES.map((s) => (
              <Label key={s} className="flex items-center gap-2 font-mono text-xs font-normal">
                <Checkbox checked={scopes.includes(s)} onCheckedChange={(on) => setScopes(on ? [...scopes, s] : scopes.filter((x) => x !== s))} />
                {s}
              </Label>
            ))}
          </div>
          <div>
            <Button type="submit" disabled={create.isPending || !user || scopes.length === 0}>Issue key</Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

/** The new key's secret, once: dropped on Done, and kept in no cache (useCreateKey). */
function Secret({ created, onDone }: { created: CreatedKey; onDone: () => void }) {
  return (
    <div className="flex flex-col gap-3 rounded-lg border border-caution/40 bg-caution/10 p-4">
      <p className="flex items-center gap-2 text-sm font-medium">
        <TriangleAlert className="size-4" aria-hidden /> Copy the secret for {created.user} now: it is shown only this once.
      </p>
      <p className="text-xs text-muted-foreground">
        The gateway keeps only its hash. Hand it over as you would a password, in a file: sandbox-cli context add NAME URL --token-file FILE.
      </p>
      <div className="flex items-center gap-2">
        <code data-testid="new-key-secret" className="min-w-0 flex-1 truncate rounded bg-background px-2 py-1 font-mono text-xs">{created.secret}</code>
        <CopyButton value={created.secret} label="Copy secret" size="sm" />
        <Button size="sm" variant="ghost" onClick={onDone}>Done</Button>
      </div>
    </div>
  );
}

function UserSSHKeys() {
  const [input, setInput] = useState("");
  const [user, setUser] = useState("");
  const { data, error } = useUserSSHKeys(user);
  const remove = useRemoveUserSSHKey();
  return (
    <section className="flex flex-col gap-3">
      <SectionHeader title="A user's SSH keys" description="Any user's registered public keys; removing one ends its logins." />
      <form className="flex gap-2" onSubmit={(e) => { e.preventDefault(); setUser(input.trim()); }}>
        <Input aria-label="SSH keys of user" placeholder="user" value={input} onChange={(e) => setInput(e.target.value)} className="w-48 font-mono" required />
        <Button type="submit" variant="outline">Show</Button>
      </form>
      {error ? <p className="text-sm text-destructive">{error.message}</p> : null}
      {user && data && (
        data.length === 0 ? (
          <p className="text-sm text-muted-foreground">{user} has no SSH keys.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Fingerprint</TableHead>
                <TableHead>Sandbox</TableHead>
                <TableHead>Added</TableHead>
                <TableHead className="text-right" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.map((k) => (
                <TableRow key={k.id}>
                  <TableCell className="font-mono text-xs" title={k.key}>{k.fingerprint}</TableCell>
                  <TableCell className="font-mono text-xs">{k.sandbox || "any of theirs"}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">{formatRelative(k.created)}</TableCell>
                  <TableCell className="text-right">
                    <Button size="sm" variant="ghost" disabled={remove.isPending}
                      onClick={() => confirm(`Remove ${k.fingerprint} from ${user}?`) && remove.mutate(k.id, { onError: (e) => toast.error(e.message) })}>
                      Remove
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )
      )}
    </section>
  );
}

export default function KeysPage() {
  return (
    <Gate need="admin">
      <Keys />
    </Gate>
  );
}
