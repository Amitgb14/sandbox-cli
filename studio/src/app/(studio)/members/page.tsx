"use client";

import { useState } from "react";
import { Building2, Users } from "lucide-react";
import { toast } from "sonner";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { Gate } from "@/components/shell/gate";
import { useCurrentOrg } from "@/components/shell/org-switcher";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ApiError } from "@/lib/api/client";
import { useOrgMembers, useOrgs, useRemoveOrgMember, useSetOrgMember } from "@/lib/api/orgs";
import { isAdmin, useCaller } from "@/lib/caller";
import { formatRelative } from "@/lib/format";
import { orgName } from "@/lib/org";
import type { OrgMember } from "@/lib/types";

/**
 * Who belongs to the current organisation. Every member may read the list;
 * an owner (or an admin key) adds and removes members and changes roles. A
 * member removed loses what they had open in the organisation at once — the
 * gateway ends their streams, SSH connections and jobs there.
 */
function Members() {
  const caller = useCaller();
  const org = useCurrentOrg() ?? "";
  const { data: orgs } = useOrgs(caller.kind === "gateway");
  const { data, error, isLoading } = useOrgMembers(org);
  const set = useSetOrgMember(org);
  const remove = useRemoveOrgMember(org);
  const [user, setUser] = useState("");
  const [tenant, setTenant] = useState("");
  const [role, setRole] = useState<"member" | "owner">("member");
  if (caller.kind !== "gateway") return null;

  const mine = orgs?.find((o) => o.name === org);
  const manage = isAdmin(caller) || mine?.role === "owner";
  const own = orgName(caller.who.tenant);
  const noList = error instanceof ApiError && error.status === 404;
  const who = (m: OrgMember) => (m.tenant ? `${m.user}@${m.tenant}` : m.user);
  const isMe = (m: OrgMember) => m.user === caller.who.user && (m.tenant ?? "") === caller.who.tenant;

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Members"
        description={
          manage
            ? `Who belongs to ${org}. Removing a member ends what they have open in it at once.`
            : `Who belongs to ${org}. Only its owners change the members.`
        }
      />
      {noList ? (
        <EmptyState
          icon={Building2}
          title={`${org} has no member list`}
          description={
            org === own
              ? "This is your key's own tenant, which the operator set when issuing the key; it is not an organization made with Create organization. Switch to one of yours, or create one, to manage members."
              : "It is not an organization you belong to."
          }
        />
      ) : (
        <>
          {manage && (
            <form
              className="flex flex-wrap items-end gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                set.mutate(
                  { user: user.trim(), tenant: tenant.trim() || undefined, role },
                  {
                    onSuccess: (m) => {
                      toast.success(`${who(m)} is ${m.role === "owner" ? "an owner" : "a member"} of ${org}`);
                      setUser("");
                      setTenant("");
                      setRole("member");
                    },
                    onError: (err) => toast.error(err.message),
                  },
                );
              }}
            >
              <Input aria-label="Member user" placeholder="user" value={user} onChange={(e) => setUser(e.target.value)} className="w-48 font-mono" required />
              <Input
                aria-label="Member tenant"
                placeholder="their tenant (default: yours)"
                value={tenant}
                onChange={(e) => setTenant(e.target.value)}
                className="w-64 font-mono"
              />
              <Select value={role} onValueChange={(v) => setRole(v as "member" | "owner")}>
                <SelectTrigger aria-label="Role" className="w-32">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="member">member</SelectItem>
                  <SelectItem value="owner">owner</SelectItem>
                </SelectContent>
              </Select>
              <Button type="submit" disabled={!user.trim() || set.isPending}>
                Add member
              </Button>
            </form>
          )}
          {error && !noList ? <p className="text-sm text-destructive">{error.message}</p> : null}
          {!isLoading && !error && (data ?? []).length === 0 ? (
            <EmptyState icon={Users} title="No members" description="Add one above." />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>User</TableHead>
                  <TableHead>Tenant</TableHead>
                  <TableHead>Role</TableHead>
                  <TableHead>Added</TableHead>
                  <TableHead className="text-right" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {(data ?? []).map((m) => (
                  <TableRow key={`${m.tenant ?? ""}/${m.user}`}>
                    <TableCell className="text-sm">
                      {m.user}
                      {isMe(m) ? <span className="ml-2 text-xs text-muted-foreground">you</span> : null}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">{orgName(m.tenant)}</TableCell>
                    <TableCell>
                      <Badge variant="outline" className={m.role === "owner" ? "border-contained/40 text-contained" : ""}>
                        {m.role}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">{m.added ? formatRelative(m.added) : "—"}</TableCell>
                    <TableCell className="text-right">
                      {manage && (
                        <div className="flex justify-end gap-1">
                          <Button
                            size="sm"
                            variant="ghost"
                            disabled={set.isPending}
                            onClick={() =>
                              set.mutate(
                                { user: m.user, tenant: m.tenant || "default", role: m.role === "owner" ? "member" : "owner" },
                                { onError: (e) => toast.error(e.message) },
                              )
                            }
                          >
                            {m.role === "owner" ? "Make member" : "Make owner"}
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            disabled={remove.isPending}
                            onClick={() =>
                              confirm(`Remove ${who(m)} from ${org}? What they have open in it ends now.`) &&
                              remove.mutate({ user: m.user, tenant: m.tenant ?? "" }, { onError: (e) => toast.error(e.message), onSuccess: () => toast.success(`Removed ${who(m)}`) })
                            }
                          >
                            Remove
                          </Button>
                        </div>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </>
      )}
    </div>
  );
}

export default function MembersPage() {
  return (
    <Gate need="gateway">
      <Members />
    </Gate>
  );
}
