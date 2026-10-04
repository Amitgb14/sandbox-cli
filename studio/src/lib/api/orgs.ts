"use client";

/**
 * A gateway's organisations, as TanStack Query hooks. Mounted only once the
 * caller is known to be a gateway, so a plain sandboxd is never asked for
 * /v1/orgs. The admin listing is in lib/admin.
 */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "@/lib/api/client";
import type { Org, OrgMember } from "@/lib/types";

const enc = encodeURIComponent;

export const okeys = {
  orgs: ["orgs"] as const,
  members: (org: string) => ["org-members", org] as const,
};

export function useOrgs(enabled: boolean) {
  return useQuery({
    queryKey: okeys.orgs,
    // The list is the key's, whatever is selected; sent without the
    // selection so a stale one cannot hide it.
    queryFn: async () => (await apiFetch<{ orgs: Org[] }>("/v1/orgs", { noOrg: true })).orgs ?? [],
    enabled,
    retry: false,
  });
}

export function useCreateOrg() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => apiFetch<Org>("/v1/orgs", { method: "POST", json: { name }, noOrg: true }),
    onSuccess: () => qc.invalidateQueries({ queryKey: okeys.orgs }),
  });
}

export function useOrgMembers(org: string) {
  return useQuery({
    queryKey: okeys.members(org),
    queryFn: async () => (await apiFetch<{ members: OrgMember[] }>(`/v1/orgs/${enc(org)}/members`, { noOrg: true })).members ?? [],
    enabled: !!org,
    retry: false,
  });
}

export function useSetOrgMember(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (m: { user: string; tenant?: string; role?: string }) =>
      apiFetch<OrgMember>(`/v1/orgs/${enc(org)}/members`, { method: "POST", json: m, noOrg: true }),
    onSuccess: () => qc.invalidateQueries({ queryKey: okeys.members(org) }),
  });
}

export function useRemoveOrgMember(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ user, tenant }: { user: string; tenant?: string }) =>
      apiFetch<void>(`/v1/orgs/${enc(org)}/members/${enc(user)}${tenant !== undefined ? `?tenant=${enc(tenant || "default")}` : ""}`, {
        method: "DELETE",
        noOrg: true,
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: okeys.members(org) }),
  });
}
