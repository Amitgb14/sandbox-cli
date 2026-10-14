"use client";

import { useState } from "react";
import { KeyRound, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { CopyButton } from "@/components/common/copy-button";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { Gate } from "@/components/shell/gate";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useCreatePersonalKey, usePersonalKeys, useRevokePersonalKey } from "@/lib/api/gateway";
import { useCaller } from "@/lib/caller";
import { formatRelative } from "@/lib/format";
import type { CreatedPersonalKey } from "@/lib/types";

const DAY = 86_400;

/** A personal key always ends: an hour to 366 days, and never after the account (the gateway caps it). */
const EXPIRIES = [
  { label: "1 day", secs: DAY },
  { label: "7 days", secs: 7 * DAY },
  { label: "30 days", secs: 30 * DAY },
  { label: "90 days", secs: 90 * DAY },
  { label: "1 year", secs: 365 * DAY },
];

/** What each scope lets the key do, in the words the form uses. */
const SCOPE_HINTS: Record<string, string> = {
  "sandbox:read": "list and read sandboxes, logs, files",
  "sandbox:create": "create sandboxes and run in them",
  "sandbox:delete": "terminate sandboxes",
  "sandbox:ssh": "SSH in, and manage SSH keys",
  "secrets:write": "set and remove secrets",
  "org:create": "create organizations",
};

function expired(at?: string): boolean {
  return !!at && new Date(at).getTime() <= Date.now();
}

/**
 * Keys the caller makes for their programs to act as them: a CI job, a
 * script, the CLI on another machine. The gateway holds each to less than
 * its maker — some of their scopes, never admin; an expiry, never past their
 * account's; their policy — and ends them with the account. The secret is in
 * one answer only, shown here once.
 */
function ApiKeys() {
  const caller = useCaller();
  const held = caller.kind === "gateway" ? caller.who.scopes.filter((s) => s !== "admin") : [];
  const policy = caller.kind === "gateway" ? caller.who.policy?.name : undefined;
  const { data, isLoading, error } = usePersonalKeys();
  const create = useCreatePersonalKey();
  const revoke = useRevokePersonalKey();
  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<string[]>(() => held.filter((s) => s === "sandbox:read" || s === "sandbox:create"));
  const [expires, setExpires] = useState(30 * DAY);
  const [created, setCreated] = useState<CreatedPersonalKey | null>(null);

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="API keys"
        description="Keys for your programs to act as you: a CI job, a script, the CLI on another machine. Each has the scopes you pick from your own and always expires, never after your account does. Your policy applies to every one, and revoking or suspending your account ends them all."
      />
      {held.length === 0 ? (
        <p className="text-sm text-muted-foreground">This key has no scopes a personal key may have: an admin&apos;s keys are issued on the Users screen.</p>
      ) : (
        <form
          className="flex flex-col gap-3 rounded-lg border p-4"
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate(
              { name: name.trim(), scopes, expires_in_secs: expires },
              {
                onSuccess: (k) => {
                  setCreated(k);
                  setName("");
                },
                onError: (err) => toast.error(err.message),
              },
            );
          }}
        >
          <div className="flex flex-wrap items-end gap-2">
            <Input aria-label="Key name" placeholder="name (ci, laptop)" value={name} onChange={(e) => setName(e.target.value)} className="w-56" maxLength={64} required />
            <Select value={String(expires)} onValueChange={(v) => setExpires(Number(v))}>
              <SelectTrigger aria-label="Key expires" className="h-9 w-40 text-sm">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {EXPIRIES.map((e) => (
                  <SelectItem key={e.secs} value={String(e.secs)}>
                    expires in {e.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button type="submit" disabled={create.isPending || !name.trim() || scopes.length === 0}>
              Create key
            </Button>
          </div>
          <fieldset className="flex flex-wrap gap-x-5 gap-y-2" aria-label="Key scopes">
            {held.map((s) => (
              <Label key={s} className="flex items-center gap-2 text-sm font-normal">
                <Checkbox
                  aria-label={s}
                  checked={scopes.includes(s)}
                  onCheckedChange={(on) => setScopes((cur) => (on === true ? [...cur, s] : cur.filter((x) => x !== s)))}
                />
                <span className="font-mono text-xs">{s}</span>
                {SCOPE_HINTS[s] ? <span className="text-xs text-muted-foreground">{SCOPE_HINTS[s]}</span> : null}
              </Label>
            ))}
          </fieldset>
          <p className="text-xs text-muted-foreground" data-testid="key-policy">
            {policy ? `Under your policy, ${policy}, as everything you do is.` : "You hold no policy: the gateway's and its nodes' limits apply."}
          </p>
        </form>
      )}
      {created ? <NewSecret created={created} onDone={() => setCreated(null)} /> : null}
      {error ? (
        <p className="text-sm text-destructive">{error.message}</p>
      ) : !isLoading && (data ?? []).length === 0 ? (
        <EmptyState icon={KeyRound} title="No API keys" description="Create one above for a program to act as you." />
      ) : (
        <Table data-testid="personal-keys">
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Scopes</TableHead>
              <TableHead>Created</TableHead>
              <TableHead>Expires</TableHead>
              <TableHead className="text-right" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {(data ?? []).map((k) => {
              const gone = k.revoked || expired(k.expires);
              return (
                <TableRow key={k.id} className={gone ? "opacity-50" : ""}>
                  <TableCell className="text-sm">
                    {k.name}
                    <div className="font-mono text-[11px] text-muted-foreground">{k.id}</div>
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1">
                      {k.scopes.map((s) => (
                        <Badge key={s} variant="outline" className="font-mono text-[11px]">{s}</Badge>
                      ))}
                    </div>
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground" title={k.created}>{formatRelative(k.created)}</TableCell>
                  <TableCell className="text-xs text-muted-foreground" title={k.expires}>
                    {k.revoked ? "revoked" : expired(k.expires) ? <span className="text-caution">expired</span> : formatRelative(k.expires)}
                  </TableCell>
                  <TableCell className="text-right">
                    {gone ? null : (
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={revoke.isPending}
                        onClick={() =>
                          confirm(`Revoke ${k.name}? Whatever uses it is refused at once.`) &&
                          revoke.mutate(k.id, { onError: (e) => toast.error(e.message), onSuccess: () => toast.success(`Revoked ${k.name}`) })
                        }
                      >
                        Revoke
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      )}
    </div>
  );
}

/** The new key's secret, once: the gateway keeps only its hash. */
function NewSecret({ created, onDone }: { created: CreatedPersonalKey; onDone: () => void }) {
  return (
    <div className="flex flex-col gap-3 rounded-lg border border-caution/40 bg-caution/10 p-4">
      <p className="flex items-center gap-2 text-sm font-medium">
        <TriangleAlert className="size-4" aria-hidden /> Copy the secret for {created.name} now: it is shown only this once.
      </p>
      <p className="text-xs text-muted-foreground">
        Keep it as you would a password, in a file or your CI&apos;s secret store: sandbox-cli context add NAME GATEWAY-URL --token-file FILE, or an
        Authorization: Bearer header.
      </p>
      <div className="flex items-center gap-2">
        <code data-testid="new-personal-key-secret" className="min-w-0 flex-1 truncate rounded bg-background px-2 py-1 font-mono text-xs">{created.secret}</code>
        <CopyButton value={created.secret} label="Copy secret" size="sm" />
        <Button size="sm" variant="ghost" onClick={onDone}>Done</Button>
      </div>
    </div>
  );
}

export default function ApiKeysPage() {
  return (
    <Gate need="gateway">
      <ApiKeys />
    </Gate>
  );
}
