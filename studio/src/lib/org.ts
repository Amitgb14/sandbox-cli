"use client";

/**
 * The organisation Studio acts in, on a gateway.
 *
 * It is a selection, not a credential: every call carries it as
 * X-Sandbox-Org (the attach WebSocket, which cannot carry headers, as
 * ?org=), and the gateway checks it against the key's memberships, answering
 * 404 for one the key may not select. Studio never decides who may act
 * where; it only remembers, per browser, which organisation was chosen.
 *
 * null is the key's own tenant: no header at all, which is exactly what a key
 * did before organisations existed. A plain sandboxd never sees a selection
 * Studio made, because none is made there (the switcher is a gateway's).
 */

import { create } from "zustand";

const ORG_KEY = "sandbox-studio-org";

function readStored(): string | null {
  if (typeof window === "undefined") return null;
  try {
    return localStorage.getItem(ORG_KEY) || null;
  } catch {
    // A browser that refuses storage just starts in the key's own tenant.
    return null;
  }
}

function writeStored(org: string | null) {
  try {
    if (org) localStorage.setItem(ORG_KEY, org);
    else localStorage.removeItem(ORG_KEY);
  } catch {
    // see readStored
  }
}

interface OrgState {
  /** The organisation selected, or null for the key's own tenant. */
  org: string | null;
  /** Whether the selection came from this browser's storage (vs the context's default). */
  stored: boolean;
  set: (org: string | null, persist: boolean) => void;
}

export const useOrgStore = create<OrgState>()((set) => {
  const stored = readStored();
  return {
    org: stored,
    stored: stored !== null,
    set: (org, persist) => {
      if (persist) writeStored(org);
      set({ org, stored: persist ? org !== null : false });
    },
  };
});

/** The current selection, for the transport (lib/api/client.ts). */
export function currentOrg(): string | null {
  return useOrgStore.getState().org;
}

/** The wire name of a tenant: the default tenant is "default". */
export function orgName(tenant: string | undefined | null): string {
  return tenant ? tenant : "default";
}

/** An organisation name the gateway will accept for a new one (internal/gateway/orgs.go). */
export function orgNameProblem(name: string): string | null {
  if (!/^[a-z][a-z0-9-]{0,29}$/.test(name)) return "1 to 30 lowercase letters, digits and dashes, starting with a letter";
  if (name.endsWith("-")) return "may not end with a dash";
  if (name.includes("--")) return 'may not contain "--"';
  if (name === "default" || name === "admin") return `"${name}" is reserved`;
  return null;
}

export const ORG_NAME_HINT = '1–30 lowercase letters, digits and dashes, starting with a letter; no "--"; not "default" or "admin".';
