import { readFileSync } from "node:fs";
import { join } from "node:path";

/**
 * The Studios global-setup.ts starts in front of the gateway, one per API key:
 * the scopes a key holds are what the tests vary, and a Studio is one
 * context's, so each key gets its own.
 */
export const GATEWAY_STUDIOS = [
  { name: "gw-admin", port: 7182, user: "ops", tenant: "", scopes: ["admin"] },
  {
    name: "gw-tenant",
    port: 7183,
    user: "alice",
    tenant: "team-a",
    scopes: ["sandbox:read", "sandbox:create", "sandbox:delete", "sandbox:ssh", "secrets:write", "org:create"],
  },
  { name: "gw-readonly", port: 7184, user: "bob", tenant: "team-a", scopes: ["sandbox:read"] },
  // A user who may not create organizations, for being added to alice's.
  { name: "gw-member", port: 7185, user: "dana", tenant: "team-a", scopes: ["sandbox:read", "sandbox:create"] },
] as const;

export type GatewayStudio = (typeof GATEWAY_STUDIOS)[number]["name"];

/**
 * The users of the hosted Studio global-setup.ts starts (`sandbox-cli studio
 * host`, serving out-hosted/ from npm run build:hosted), each in a tenant of
 * their own. gil's key is revoked by a test, so nobody else depends on it.
 */
export const HOSTED_PORT = 7186;
export const HOSTED_USERS = ["hana", "ivo", "gil"] as const;
export type HostedUser = (typeof HOSTED_USERS)[number];

export interface E2EState {
  /** The Studio in front of the plain sandboxd, on 7181. */
  token: string;
  /** Each gateway Studio's token, by name. */
  gateway: Partial<Record<GatewayStudio, string>>;
  pids: number[];
  dir: string;
  /** The hosted Studio's users, when out-hosted/ was built: invite link, key and key id. */
  hosted?: Record<HostedUser, { invite: string; key: string; id: string }>;
  /** The gateway's address and its admin key, for what a test does as the operator. */
  gatewayURL: string;
  adminKey: string;
}

export function e2eState(): E2EState {
  return JSON.parse(readFileSync(join(__dirname, ".state.json"), "utf8")) as E2EState;
}

/** The address of a gateway Studio, and its token. */
export function gatewayStudio(name: GatewayStudio): { base: string; token: string } {
  const s = GATEWAY_STUDIOS.find((x) => x.name === name)!;
  return { base: `http://127.0.0.1:${s.port}`, token: e2eState().gateway[name]! };
}
