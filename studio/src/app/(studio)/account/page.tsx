"use client";

import { useState } from "react";
import { toast } from "sonner";
import { PageHeader, SectionHeader } from "@/components/common/page-header";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { apiFetch } from "@/lib/api/client";
import { Gate } from "@/components/shell/gate";
import { Badge } from "@/components/ui/badge";
import { useCaller } from "@/lib/caller";
import { useCurrentOrg } from "@/components/shell/org-switcher";
import { cn } from "@/lib/utils";

/** What each scope lets a key do (docs/fleet.md, "Giving users keys"). */
const SCOPES: [string, string][] = [
  ["sandbox:read", "See your sandboxes, their output, files and events; list volumes, snapshots, jobs, services and secret names"],
  ["sandbox:create", "Create sandboxes and volumes and act in them; submit jobs; deploy and scale services"],
  ["sandbox:delete", "Terminate sandboxes; delete volumes, snapshots and services"],
  ["sandbox:ssh", "Register SSH keys, issue SSH access tokens, and log in over SSH"],
  ["secrets:write", "Set and remove the tenant's secrets"],
  ["org:create", "Create organizations, each with its own sandboxes, secrets and quota"],
  ["admin", "Every scope, on every user's sandboxes, plus keys, nodes and the audit record"],
];

/**
 * Who this Studio is, as the gateway sees the context's API key. The key
 * itself is the context's (sandbox-cli context add … --token-file) and never
 * reaches the browser: `sandbox-cli studio` holds it.
 */
function Account() {
  const caller = useCaller();
  const org = useCurrentOrg();
  if (caller.kind !== "gateway") return null;
  const { who } = caller;
  const holds = (s: string) => who.scopes.includes(s) || who.scopes.includes("admin");
  return (
    <div className="flex flex-col gap-8">
      <PageHeader title="Account" description="The API key this Studio's context holds. The key stays with sandbox-cli; the browser never sees it." />
      <dl className="grid max-w-xl grid-cols-[10rem_1fr] gap-y-1.5 text-sm">
        <dt className="text-muted-foreground">user</dt>
        <dd className="font-mono">{who.user}</dd>
        <dt className="text-muted-foreground">role</dt>
        <dd data-testid="account-role">
          {who.scopes.includes("admin") ? "super admin: every organization on this gateway" : <>admin of <span className="font-mono">{org}</span></>}
        </dd>
        {who.email && (
          <>
            <dt className="text-muted-foreground">email</dt>
            <dd data-testid="account-email">{who.email}</dd>
          </>
        )}
        {who.policy ? (
          <>
            <dt className="text-muted-foreground">policy</dt>
            <dd data-testid="account-policy">
              <span className="font-mono">{who.policy.name}</span>
              {who.policy.max_lifetime_secs ? (
                <span className="text-muted-foreground"> — each sandbox ends {Math.round(who.policy.max_lifetime_secs / 60)} min after it starts</span>
              ) : null}
            </dd>
          </>
        ) : null}
        <dt className="text-muted-foreground">tenant</dt>
        <dd className="font-mono">{who.tenant || <span className="text-muted-foreground">the default tenant</span>}</dd>
        <dt className="text-muted-foreground">organization</dt>
        <dd className="font-mono" data-testid="account-org">{org}</dd>
        <dt className="text-muted-foreground">key</dt>
        <dd className="font-mono">{who.key_id}</dd>
      </dl>
      {who.email && <ChangePassword />}
      <section className="flex flex-col gap-3">
        <SectionHeader title="Scopes" description="Fixed when the key was issued. Studio hides what they do not allow; the gateway refuses it." />
        <ul className="flex max-w-3xl flex-col divide-y rounded-lg border">
          {SCOPES.map(([s, what]) => (
            <li key={s} className="flex items-baseline gap-3 px-4 py-2.5 text-sm">
              <Badge variant="outline" className={cn("w-32 shrink-0 justify-center font-mono", holds(s) ? "border-contained/40 text-contained" : "text-muted-foreground line-through")}>
                {s}
              </Badge>
              <span className={holds(s) ? "" : "text-muted-foreground"}>{what}</span>
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}

/**
 * An account's own change of password, at the gateway with this session's
 * key. Every other sign-in of the account ends; this one stays.
 */
function ChangePassword() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <section className="flex flex-col gap-3">
      <SectionHeader title="Password" description="Changing it signs you out everywhere else." />
      <form
        className="flex max-w-xl flex-col gap-3"
        aria-label="Change password"
        onSubmit={(e) => {
          e.preventDefault();
          setBusy(true);
          apiFetch("/v1/auth/password", { method: "POST", json: { current, new: next }, noOrg: true })
            .then(() => {
              toast.success("Password changed");
              setCurrent("");
              setNext("");
            })
            .catch((err: Error) => toast.error(err.message))
            .finally(() => setBusy(false));
        }}
      >
        <input type="email" autoComplete="username" hidden readOnly />
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="current-password">Current password</Label>
          <Input id="current-password" type="password" autoComplete="current-password" required value={current} onChange={(e) => setCurrent(e.target.value)} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="next-password">New password</Label>
          <Input id="next-password" type="password" autoComplete="new-password" minLength={10} required value={next} onChange={(e) => setNext(e.target.value)} />
        </div>
        <div>
          <Button type="submit" disabled={busy}>Change password</Button>
        </div>
      </form>
    </section>
  );
}

export default function AccountPage() {
  return (
    <Gate need="gateway">
      <Account />
    </Gate>
  );
}
