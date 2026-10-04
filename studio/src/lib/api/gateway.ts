"use client";

/**
 * A gateway's tenant endpoints — jobs, services, secrets, SSH — as TanStack
 * Query hooks, the same shape as queries.ts. Every one is mounted only behind
 * a Gate (components/shell/gate.tsx), so a plain sandboxd is never asked.
 * The admin endpoints are in lib/admin, which only admin screens import.
 */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, apiFetch, authHeaders } from "@/lib/api/client";
import type { Job, JobOutput, JobSpec, SecretInfo, Service, ServiceSpec, SSHAccess, SSHInfo, SSHKey } from "@/lib/types";

const enc = encodeURIComponent;

export const gkeys = {
  jobs: ["jobs"] as const,
  job: (id: string) => ["job", id] as const,
  jobOutput: (id: string, n: number) => ["job-output", id, n] as const,
  services: ["services"] as const,
  service: (name: string, tenant?: string) => ["service", name, tenant ?? ""] as const,
  secrets: ["secrets"] as const,
  ssh: ["ssh"] as const,
  sshKeys: ["ssh-keys"] as const,
};

/** ?tenant=T, which an admin uses to name another tenant's service. */
const tenantQ = (tenant?: string) => (tenant ? `?tenant=${enc(tenant)}` : "");

// --- jobs ----------------------------------------------------------------------------

export function useJobs() {
  return useQuery({
    queryKey: gkeys.jobs,
    queryFn: async () => (await apiFetch<{ jobs: Job[] }>("/v1/jobs")).jobs ?? [],
    refetchInterval: 5_000,
  });
}

export function useJob(id: string) {
  return useQuery({
    queryKey: gkeys.job(id),
    queryFn: () => apiFetch<Job>(`/v1/jobs/${enc(id)}`),
    enabled: !!id,
    refetchInterval: (q) => (q.state.data?.state === "running" ? 3_000 : false),
    retry: false,
  });
}

export function useJobOutput(id: string, n: number, enabled: boolean) {
  return useQuery({
    queryKey: gkeys.jobOutput(id, n),
    queryFn: () => apiFetch<JobOutput>(`/v1/jobs/${enc(id)}/runs/${n}/output`),
    enabled,
    retry: false,
  });
}

function useInvalidating<V, R>(fn: (v: V) => Promise<R>, invalidate: (v: V) => readonly (readonly unknown[])[]) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: (_r, v) => {
      for (const k of invalidate(v)) qc.invalidateQueries({ queryKey: k });
    },
  });
}

export function useSubmitJob() {
  return useInvalidating((spec: JobSpec) => apiFetch<Job>("/v1/jobs", { method: "POST", json: spec }), () => [gkeys.jobs]);
}

export function useCancelJob() {
  return useInvalidating(
    (id: string) => apiFetch<Job>(`/v1/jobs/${enc(id)}`, { method: "DELETE" }),
    (id) => [gkeys.jobs, gkeys.job(id)],
  );
}

/**
 * Saves a file kept from a run. It is fetched with the Studio token as any
 * other call, then handed to the browser as a download: a plain link could
 * not carry the token.
 */
export async function downloadJobFile(id: string, n: number, path: string): Promise<void> {
  const resp = await fetch(`/api/v1/jobs/${enc(id)}/runs/${n}/files?path=${enc(path)}`, {
    headers: authHeaders(),
  });
  if (!resp.ok) throw new ApiError(resp.status, resp.statusText);
  const url = URL.createObjectURL(await resp.blob());
  const a = document.createElement("a");
  a.href = url;
  a.download = path.split("/").filter(Boolean).pop() || "file";
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

// --- services ------------------------------------------------------------------------

/** The link to a service's page; an admin names another tenant's with tenant=. */
export function serviceHref(s: Service): string {
  const q = new URLSearchParams({ name: s.spec.name });
  if (s.tenant) q.set("tenant", s.tenant);
  return `/service?${q.toString()}`;
}

export function useServices() {
  return useQuery({
    queryKey: gkeys.services,
    queryFn: async () => (await apiFetch<{ services: Service[] }>("/v1/services")).services ?? [],
    refetchInterval: 5_000,
  });
}

export function useService(name: string, tenant?: string) {
  return useQuery({
    queryKey: gkeys.service(name, tenant),
    queryFn: () => apiFetch<Service>(`/v1/services/${enc(name)}${tenantQ(tenant)}`),
    enabled: !!name,
    refetchInterval: 3_000,
    retry: false,
  });
}

/** Creates the service, or with update, replaces the spec of the one by that name — as `service deploy` does. */
export function useDeployService() {
  return useInvalidating(
    ({ spec, update }: { spec: ServiceSpec; update: boolean }) =>
      update
        ? apiFetch<Service>(`/v1/services/${enc(spec.name)}`, { method: "PUT", json: spec })
        : apiFetch<Service>("/v1/services", { method: "POST", json: spec }),
    ({ spec }) => [gkeys.services, ["service", spec.name]],
  );
}

export function useScaleService() {
  return useInvalidating(
    ({ name, tenant, replicas }: { name: string; tenant?: string; replicas: number }) =>
      apiFetch<Service>(`/v1/services/${enc(name)}/scale${tenantQ(tenant)}`, { method: "POST", json: { replicas } }),
    ({ name }) => [gkeys.services, ["service", name]],
  );
}

export function useRemoveService() {
  return useInvalidating(
    ({ name, tenant }: { name: string; tenant?: string }) =>
      apiFetch<void>(`/v1/services/${enc(name)}${tenantQ(tenant)}`, { method: "DELETE" }),
    () => [gkeys.services],
  );
}

// --- secrets: names only; a value goes in and never comes back --------------------------

export function useSecrets() {
  return useQuery({
    queryKey: gkeys.secrets,
    queryFn: async () => (await apiFetch<{ secrets: SecretInfo[] }>("/v1/secrets")).secrets ?? [],
    retry: false,
  });
}

export function useSetSecret() {
  return useInvalidating(
    ({ name, value }: { name: string; value: string }) =>
      apiFetch<void>(`/v1/secrets/${enc(name)}`, { method: "PUT", json: { value } }),
    () => [gkeys.secrets],
  );
}

export function useRemoveSecret() {
  return useInvalidating(
    (name: string) => apiFetch<void>(`/v1/secrets/${enc(name)}`, { method: "DELETE" }),
    () => [gkeys.secrets],
  );
}

// --- SSH -------------------------------------------------------------------------------

/** Where the gateway's SSH server listens; null when it serves none (404). */
export function useSSHInfo() {
  return useQuery({
    queryKey: gkeys.ssh,
    queryFn: async () => {
      try {
        return await apiFetch<SSHInfo>("/v1/ssh");
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }
    },
    retry: false,
  });
}

export function useSSHKeys(enabled: boolean) {
  return useQuery({
    queryKey: gkeys.sshKeys,
    queryFn: async () => (await apiFetch<{ keys: SSHKey[] }>("/v1/ssh-keys")).keys ?? [],
    enabled,
    retry: false,
  });
}

export function useAddSSHKey() {
  return useInvalidating(
    ({ key, sandbox }: { key: string; sandbox?: string }) =>
      apiFetch<SSHKey>("/v1/ssh-keys", { method: "POST", json: sandbox ? { key, sandbox } : { key } }),
    () => [gkeys.sshKeys],
  );
}

export function useRemoveSSHKey() {
  return useInvalidating(
    (id: string) => apiFetch<void>(`/v1/ssh-keys/${enc(id)}`, { method: "DELETE" }),
    () => [gkeys.sshKeys],
  );
}

/** The token in the answer is a credential: gcTime 0 keeps it out of the mutation cache. */
export function useSSHAccess() {
  return useMutation({
    mutationFn: ({ sandbox, ttl_secs }: { sandbox: string; ttl_secs: number }) =>
      apiFetch<SSHAccess>(`/v1/sandboxes/${enc(sandbox)}/ssh-access`, { method: "POST", json: { ttl_secs } }),
    gcTime: 0,
  });
}
