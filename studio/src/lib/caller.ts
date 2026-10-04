"use client";

/**
 * Who Studio is talking as, which decides the screens.
 *
 * One Studio serves both a plain sandboxd and a gateway. On load it asks
 * GET /v1/whoami: a plain sandboxd answers 404 and Studio shows exactly the
 * screens it always had; a gateway answers with the key's user, tenant and
 * scopes, and Studio adds the tenant screens — and the admin ones for an
 * admin key — and hides every action the scopes do not allow.
 *
 * Hiding is cosmetic. The gateway refuses a call without its scope (403)
 * before anything is looked up; that refusal is the control, and this only
 * keeps a button from offering what will be refused.
 */

import { useQuery } from "@tanstack/react-query";
import { ApiError, apiFetch } from "@/lib/api/client";
import type { Scope, Whoami } from "@/lib/types";

/**
 * Whether this build has the admin screens. `NEXT_PUBLIC_STUDIO_ADMIN=off` at
 * build time makes it false; Next inlines the value, so every branch behind it
 * is dead code the minifier removes, and the admin pages themselves are not
 * routes in such a build (next.config.ts). For a hosted, multi-tenant
 * dashboard, where no visitor should be handed the operator's UI at all.
 */
export const ADMIN_BUILD = process.env.NEXT_PUBLIC_STUDIO_ADMIN !== "off";

export type Caller =
  /** Not known yet: whoami has not answered. */
  | { kind: "loading" }
  /** A plain sandboxd (whoami 404), or one whose answer said nothing: today's screens. */
  | { kind: "sandboxd" }
  | { kind: "gateway"; who: Whoami };

export const whoamiKey = ["whoami"] as const;

export function useCaller(): Caller {
  const { data, isPending } = useQuery({
    queryKey: whoamiKey,
    queryFn: async (): Promise<Whoami | null> => {
      try {
        return await apiFetch<Whoami>("/v1/whoami");
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }
    },
    retry: false,
    // A key's scopes are fixed at issue; a revoked key fails every call anyway.
    staleTime: 60_000,
  });
  if (isPending) return { kind: "loading" };
  if (!data) return { kind: "sandboxd" };
  return { kind: "gateway", who: data };
}

/**
 * Whether the caller may do what scope covers. A plain sandboxd has no scopes:
 * whoever holds its token is its operator. While unknown, nothing is offered.
 */
export function can(caller: Caller, scope: Scope): boolean {
  if (caller.kind === "sandboxd") return true;
  if (caller.kind === "loading") return false;
  return caller.who.scopes.includes(scope) || caller.who.scopes.includes("admin");
}

export function isGateway(caller: Caller): caller is { kind: "gateway"; who: Whoami } {
  return caller.kind === "gateway";
}

export function isAdmin(caller: Caller): boolean {
  return ADMIN_BUILD && caller.kind === "gateway" && caller.who.scopes.includes("admin");
}

/** useCaller and can, together, for a component that asks several times. */
export function useCan(): (scope: Scope) => boolean {
  const caller = useCaller();
  return (scope) => can(caller, scope);
}
