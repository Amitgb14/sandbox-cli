"use client";

import { useState } from "react";
import { FolderPlus, Layers } from "lucide-react";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { GroupCard, type GroupStore } from "@/components/settings/group-card";
import { Gate } from "@/components/shell/gate";
import { Button } from "@/components/ui/button";
import { useAdminEgressGroups, useDeleteEgressGroup, usePutEgressGroup } from "@/lib/admin/api";

/**
 * The gateway's groups: the names the gateway takes (internal/gateway), no
 * default flag, and at least one host, so a new group is made with its first.
 */
const GATEWAY_GROUPS: GroupStore = {
  useSave: usePutEgressGroup,
  useRemove: useDeleteEgressGroup,
  name: { pattern: /^[a-z][a-z0-9-]{0,29}$/, hint: "A lowercase letter, then letters, digits and dashes, up to 30." },
  withDefault: false,
  hostsOnCreate: true,
  createLabel: "Create",
};

/**
 * Allowlist groups on the gateway: named sets of hosts every user may pick
 * for a sandbox on an allowlist, in the Playground or when changing a running
 * sandbox's network. The gateway expands a group when the sandbox is made or
 * changed, so an edit here reaches the next launch, not one already running.
 * None is ever picked for a user who did not pick it: there is no default, so
 * widening a group widens only the launches that ask for it.
 */
function EgressGroups() {
  const { data, isLoading, error } = useAdminEgressGroups();
  const [adding, setAdding] = useState(false);
  const groups = data ?? [];
  const taken = groups.map((g) => g.name);
  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Allowlist groups"
        description="Named sets of hosts any user may pick for a sandbox on an allowlist. A group is only ever added to a launch that names it; one a running job or a service names cannot be deleted."
        actions={
          <Button size="sm" variant="outline" className="gap-1.5" onClick={() => setAdding(true)} disabled={adding}>
            <FolderPlus className="size-4" /> New group
          </Button>
        }
      />
      {error ? <p className="text-sm text-destructive">{error.message}</p> : null}
      <div className="grid max-w-5xl gap-3 md:grid-cols-2">
        {adding ? <GroupCard store={GATEWAY_GROUPS} taken={taken} onDone={() => setAdding(false)} /> : null}
        {groups.map((g) => (
          <GroupCard key={g.name} store={GATEWAY_GROUPS} group={g} taken={taken} />
        ))}
      </div>
      {!adding && !isLoading && !error && groups.length === 0 ? (
        <EmptyState icon={Layers} title="No groups" description="Make one per purpose — go, npm, an internal registry — for users to pick in the Playground." />
      ) : null}
    </div>
  );
}

export default function AdminEgressGroupsPage() {
  return (
    <Gate need="admin">
      <EgressGroups />
    </Gate>
  );
}
