"use client";

import { PageHeader, SectionHeader } from "@/components/common/page-header";
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
        <dt className="text-muted-foreground">tenant</dt>
        <dd className="font-mono">{who.tenant || <span className="text-muted-foreground">the default tenant</span>}</dd>
        <dt className="text-muted-foreground">organization</dt>
        <dd className="font-mono" data-testid="account-org">{org}</dd>
        <dt className="text-muted-foreground">key</dt>
        <dd className="font-mono">{who.key_id}</dd>
      </dl>
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

export default function AccountPage() {
  return (
    <Gate need="gateway">
      <Account />
    </Gate>
  );
}
