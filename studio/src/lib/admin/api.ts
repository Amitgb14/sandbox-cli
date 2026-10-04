"use client";

/**
 * The gateway's admin endpoints (/v1/admin/*, docs/api/v1.md "Gateway"),
 * imported by the admin screens and nothing else — so a build with
 * NEXT_PUBLIC_STUDIO_ADMIN=off, which has no admin screens, has none of this
 * either.
 *
 * Two things never reach a component from here: a node's endpoint (where the
 * private network is) and a key's secret after the one answer that carries
 * it. The endpoint is dropped as each node is read, and taken out of the
 * node's error text too, which a failed dial fills with it.
 */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "@/lib/api/client";
import type { Capabilities, SSHKey } from "@/lib/types";

const enc = encodeURIComponent;

export interface NodeResources {
  cpus: number;
  memory_mb: number;
  disk_mb: number;
}

/** A node as Studio keeps it: no endpoint. */
export interface Node {
  name: string;
  healthy: boolean;
  last_seen?: string;
  error?: string;
  status?: {
    node: string;
    version: string;
    capabilities: Capabilities;
    capacity: NodeResources;
    free: NodeResources;
    running: number;
    pooled?: Record<string, number>;
    images?: string[];
    labels?: Record<string, string>;
    cordoned: boolean;
  };
}

export interface NodeSpec {
  name: string;
  endpoint: string;
  token_file?: string;
  ca_file?: string;
  cert_file?: string;
  key_file?: string;
}

export interface DrainResult {
  node: string;
  cordoned: boolean;
  remaining: number;
  terminated: string[];
  failed?: { id: string; error: string }[];
}

export interface LostSandbox {
  id: string;
  user: string;
  tenant: string;
  node: string;
  cpus?: number;
  memory_mb?: number;
  node_down_since?: string;
}

export interface KeyInfo {
  id: string;
  user: string;
  tenant: string;
  scopes: string[];
  created: string;
  revoked?: boolean;
}

/** POST /v1/admin/keys: the one answer with the secret in it. */
export interface CreatedKey extends KeyInfo {
  secret: string;
}

export interface AuditEntry {
  time: string;
  kind: "api" | "ssh";
  action: string;
  key_id?: string;
  user?: string;
  tenant?: string;
  remote?: string;
  sandbox?: string;
  node?: string;
  target?: string;
  status?: number;
  result?: string;
  fingerprint?: string;
  session?: string;
}

export const akeys = {
  nodes: ["admin", "nodes"] as const,
  lost: ["admin", "lost"] as const,
  keys: ["admin", "keys"] as const,
  sshKeys: (user: string) => ["admin", "ssh-keys", user] as const,
  audit: (since: string) => ["admin", "audit", since] as const,
};

/** A node without its endpoint, in its fields or in its error. */
function scrub(n: NodeSpec & Omit<Node, "name">): Node {
  const { endpoint, ...rest } = n;
  let error = rest.error;
  if (error && endpoint) {
    const host = endpoint.replace(/^[a-z]+:\/\//, "");
    for (const s of [endpoint, host, host.replace(/:\d+$/, "")].filter((s) => s.length > 2)) {
      error = error.split(s).join("…");
    }
  }
  return { name: rest.name, healthy: rest.healthy, last_seen: rest.last_seen, error, status: rest.status };
}

export function useNodes() {
  return useQuery({
    queryKey: akeys.nodes,
    queryFn: async () => ((await apiFetch<{ nodes: (NodeSpec & Omit<Node, "name">)[] }>("/v1/admin/nodes")).nodes ?? []).map(scrub),
    refetchInterval: 5_000,
  });
}

export function useLost() {
  return useQuery({
    queryKey: akeys.lost,
    queryFn: async () => (await apiFetch<{ sandboxes: LostSandbox[] }>("/v1/admin/lost")).sandboxes ?? [],
    refetchInterval: 10_000,
  });
}

export function useKeys() {
  return useQuery({
    queryKey: akeys.keys,
    queryFn: async () => (await apiFetch<{ keys: KeyInfo[] }>("/v1/admin/keys")).keys ?? [],
  });
}

export function useUserSSHKeys(user: string) {
  return useQuery({
    queryKey: akeys.sshKeys(user),
    queryFn: async () => (await apiFetch<{ keys: SSHKey[] }>(`/v1/admin/ssh-keys?user=${enc(user)}`)).keys ?? [],
    enabled: !!user,
    retry: false,
  });
}

export function useAudit(since: string) {
  return useQuery({
    queryKey: akeys.audit(since),
    queryFn: () => apiFetch<{ entries: AuditEntry[]; truncated?: boolean }>(`/v1/admin/audit?since=${enc(since)}&limit=1000`),
    retry: false,
  });
}

function useAdminMutation<V, R>(fn: (v: V) => Promise<R>, invalidate: readonly unknown[]) {
  const qc = useQueryClient();
  return useMutation({ mutationFn: fn, onSuccess: () => qc.invalidateQueries({ queryKey: invalidate }) });
}

export function useAddNode() {
  return useAdminMutation(
    async (spec: NodeSpec) => scrub(await apiFetch<NodeSpec & Omit<Node, "name">>("/v1/admin/nodes", { method: "POST", json: spec })),
    akeys.nodes,
  );
}

export function useRemoveNode() {
  return useAdminMutation((name: string) => apiFetch<void>(`/v1/admin/nodes/${enc(name)}`, { method: "DELETE" }), akeys.nodes);
}

export function useCordon() {
  return useAdminMutation(
    async ({ name, cordoned }: { name: string; cordoned: boolean }) => {
      await apiFetch<unknown>(`/v1/admin/nodes/${enc(name)}/cordon`, { method: "POST", json: { cordoned } });
    },
    akeys.nodes,
  );
}

export function useDrain() {
  return useAdminMutation(
    ({ name, terminate }: { name: string; terminate: boolean }) =>
      apiFetch<DrainResult>(`/v1/admin/nodes/${enc(name)}/drain`, { method: "POST", json: { terminate } }),
    akeys.nodes,
  );
}

/** gcTime 0: the answer carrying the secret is not kept in the mutation cache once nothing shows it. */
export function useCreateKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: { user: string; tenant?: string; scopes: string[] }) => apiFetch<CreatedKey>("/v1/admin/keys", { method: "POST", json: req }),
    onSuccess: () => qc.invalidateQueries({ queryKey: akeys.keys }),
    gcTime: 0,
  });
}

export function useRevokeKey() {
  return useAdminMutation((id: string) => apiFetch<void>(`/v1/admin/keys/${enc(id)}`, { method: "DELETE" }), akeys.keys);
}

export function useRemoveUserSSHKey() {
  return useAdminMutation((id: string) => apiFetch<void>(`/v1/admin/ssh-keys/${enc(id)}`, { method: "DELETE" }), ["admin", "ssh-keys"]);
}
