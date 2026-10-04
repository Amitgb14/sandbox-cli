"use client";

import { Fragment, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { Building2, Check, ChevronsUpDown, Plus, Users } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ApiError, apiFetch } from "@/lib/api/client";
import { useInfo } from "@/lib/api/queries";
import { useCreateOrg, useOrgs } from "@/lib/api/orgs";
import { can, useCaller } from "@/lib/caller";
import { ORG_NAME_HINT, orgName, orgNameProblem, useOrgStore } from "@/lib/org";
import type { Whoami } from "@/lib/types";

/**
 * Switching organisation: the selection changes, and everything Studio holds
 * of the one before is dropped — the query cache cleared, in-flight requests
 * cancelled, and the screens remounted (OrgScope) — so not one row of the
 * previous organisation is shown under the new one's name.
 */
export function useSwitchOrg() {
  const qc = useQueryClient();
  const set = useOrgStore((s) => s.set);
  return (org: string | null, persist = true) => {
    void qc.cancelQueries();
    qc.clear();
    set(org, persist);
  };
}

/** The organisation shown as current: the selection, or the key's own tenant. */
export function useCurrentOrg(): string | null {
  const caller = useCaller();
  const org = useOrgStore((s) => s.org);
  if (caller.kind !== "gateway") return null;
  return org ?? orgName(caller.who.tenant);
}

/** Remounts what it wraps whenever the organisation changes (see useSwitchOrg). */
export function OrgScope({ children }: { children: React.ReactNode }) {
  const org = useOrgStore((s) => s.org);
  return <Fragment key={org ?? ""}>{children}</Fragment>;
}

/**
 * Keeps the selection honest, on a gateway only. It starts in the context's
 * organisation (sandbox-cli org use) when this browser has chosen none, and
 * when the gateway refuses the selection — a membership removed, an
 * organisation that is not there — it falls back to the key's own tenant and
 * says so, rather than showing every screen empty.
 */
export function OrgSync() {
  const caller = useCaller();
  const { data: info } = useInfo();
  const { org, stored } = useOrgStore();
  const switchOrg = useSwitchOrg();
  const applied = useRef(false);
  const gateway = caller.kind === "gateway";

  useEffect(() => {
    if (!gateway || applied.current || !info) return;
    applied.current = true;
    if (!stored && org === null && info.org) switchOrg(info.org, false);
  }, [gateway, info, stored, org, switchOrg]);

  const check = useQuery({
    queryKey: ["org-check", org],
    queryFn: () => apiFetch<Whoami>("/v1/whoami"),
    enabled: gateway && org !== null,
    retry: false,
    staleTime: 60_000,
  });
  const refused = check.error instanceof ApiError && check.error.status === 404;
  useEffect(() => {
    if (!refused || caller.kind !== "gateway" || org === null) return;
    toast.warning(`Organization ${org} is not available to this key; showing ${orgName(caller.who.tenant)} instead.`);
    switchOrg(null);
  }, [refused, caller, org, switchOrg]);
  return null;
}

/** The organisation switcher at the top of the sidebar; nothing on a plain sandboxd. */
export function OrgSwitcher() {
  const caller = useCaller();
  const gateway = caller.kind === "gateway";
  const { data: orgs } = useOrgs(gateway);
  const current = useCurrentOrg();
  const switchOrg = useSwitchOrg();
  const [creating, setCreating] = useState(false);
  if (caller.kind !== "gateway" || !current) return null;
  const own = orgName(caller.who.tenant);
  const mayCreate = can(caller, "org:create");

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            aria-label={`Organization: ${current}`}
            data-testid="org-switcher"
            className="flex h-9 items-center gap-2 rounded-md border bg-background px-2.5 text-left text-sm transition-colors hover:bg-muted/60"
          >
            <Building2 className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1 truncate font-medium">{current}</span>
            <ChevronsUpDown className="size-3.5 shrink-0 text-muted-foreground" />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="w-(--radix-dropdown-menu-trigger-width) min-w-56">
          <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">Organizations</DropdownMenuLabel>
          {(orgs ?? [{ name: own, role: "member" as const, current: true }]).map((o) => (
            <DropdownMenuItem key={o.name} onSelect={() => o.name !== current && switchOrg(o.name === own ? null : o.name)}>
              <Check className={o.name === current ? "size-4" : "size-4 opacity-0"} />
              <span className="min-w-0 flex-1 truncate">{o.name}</span>
              <span className="text-xs text-muted-foreground">{o.name === own ? "your key's" : o.role}</span>
            </DropdownMenuItem>
          ))}
          <DropdownMenuSeparator />
          {mayCreate && (
            <DropdownMenuItem onSelect={() => setCreating(true)}>
              <Plus className="size-4" />
              Create organization
            </DropdownMenuItem>
          )}
          <DropdownMenuItem asChild>
            <Link href="/members">
              <Users className="size-4" />
              Members
            </Link>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      {mayCreate && <CreateOrgDialog open={creating} onOpenChange={setCreating} onCreated={(name) => switchOrg(name)} />}
    </>
  );
}

function CreateOrgDialog({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (o: boolean) => void; onCreated: (name: string) => void }) {
  const create = useCreateOrg();
  const [name, setName] = useState("");
  const problem = name ? orgNameProblem(name) : null;
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) setName("");
        onOpenChange(o);
      }}
    >
      <DialogContent className="sm:max-w-md">
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (orgNameProblem(name)) return;
            create.mutate(name, {
              onSuccess: (o) => {
                toast.success(`Created ${o.name}; you are its owner`);
                setName("");
                onOpenChange(false);
                onCreated(o.name);
              },
              onError: (err) => toast.error(err.message),
            });
          }}
        >
          <DialogHeader>
            <DialogTitle>Create organization</DialogTitle>
            <DialogDescription>
              An organization has its own sandboxes, secrets, jobs, services and quota, which nobody outside it can see. You become its owner and can add members.
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="org-name">Organization name</Label>
            <Input id="org-name" value={name} onChange={(e) => setName(e.target.value.trim())} className="font-mono" autoFocus required />
            <p className={problem ? "text-xs text-destructive" : "text-xs text-muted-foreground"}>{problem ?? ORG_NAME_HINT}</p>
          </div>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={!name || !!problem || create.isPending}>
              Create
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
