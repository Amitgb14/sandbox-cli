"use client";

import { Suspense } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Building2 } from "lucide-react";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { MembersOf } from "@/components/org/members";
import { Gate } from "@/components/shell/gate";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useAdminOrgs } from "@/lib/admin/api";

/**
 * The super admin's members screen: any organisation's members, picked from
 * every one on the gateway (?org=, so All organizations links here), without
 * switching into it. The gateway lets an admin key manage any
 * organisation's members; a user's own first tenant has no member list.
 */
function AdminMembers() {
  const { data, isLoading, error } = useAdminOrgs();
  const params = useSearchParams();
  const router = useRouter();
  const orgs = data ?? [];
  const org = params.get("org") ?? orgs[0]?.name ?? "";
  if (error) return <p className="text-sm text-destructive">{error.message}</p>;
  if (!isLoading && orgs.length === 0) {
    return (
      <div className="flex flex-col gap-5">
        <PageHeader title="Members" description="Who belongs to each organization on the gateway." />
        <EmptyState
          icon={Building2}
          title="No organizations"
          description="A user's first tenant has no member list. Create an organization (the switcher at the top of the sidebar) to share one with others."
        />
      </div>
    );
  }
  const picker = (
    <Select value={org} onValueChange={(v) => router.replace(`/admin/members/?org=${encodeURIComponent(v)}`)}>
      <SelectTrigger aria-label="Organization" className="w-64 font-mono">
        <SelectValue placeholder="organization" />
      </SelectTrigger>
      <SelectContent>
        {orgs.map((o) => (
          <SelectItem key={o.name} value={o.name}>
            {o.name}
            <span className="ml-2 text-xs text-muted-foreground">{o.members} members</span>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
  return org ? <MembersOf key={org} org={org} picker={picker} /> : null;
}

export default function AdminMembersPage() {
  return (
    <Gate need="admin">
      <Suspense fallback={<p className="text-sm text-muted-foreground">Loading…</p>}>
        <AdminMembers />
      </Suspense>
    </Gate>
  );
}
