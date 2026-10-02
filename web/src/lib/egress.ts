/**
 * Traffic the egress visualiser sends at the allowlist. Verdicts follow the
 * real rule in docs/api/v1.md and docs/self-hosting.md: a default sandboxd's
 * policy is an allowlist — a baseline of agent APIs and package registries,
 * plus the names a request adds and the server permits — checked by name on
 * the host, outside the guest, with deny winning over allow.
 */

export type Verdict = "baseline" | "allowed" | "blocked";

export type Destination = {
  host: string;
  what: string;
  /** Which bucket it lands in when the allowlist is on. */
  verdict: Verdict;
  /** True for the ones that make the case — shown first. */
  headline?: boolean;
};

export const DESTINATIONS: Destination[] = [
  { host: "api.anthropic.com", what: "the model the agent is running on", verdict: "baseline", headline: true },
  { host: "registry.npmjs.org", what: "npm install, still working", verdict: "baseline", headline: true },
  { host: "github.com", what: "git fetch, git push", verdict: "baseline", headline: true },
  { host: "pypi.org", what: "pip install", verdict: "baseline" },
  { host: "files.pythonhosted.org", what: "the wheels themselves", verdict: "baseline" },
  { host: "raw.githubusercontent.com", what: "install scripts", verdict: "baseline" },
  {
    host: "internal.registry.example.com",
    what: "your private registry — added with --allow, where the server's policy permits",
    verdict: "allowed",
    headline: true,
  },
  { host: "api.continue.dev", what: "an agent's own config endpoint, added with --allow", verdict: "allowed" },
  {
    host: "paste.example.net",
    what: "the exfiltration a prompt-injected agent was talked into",
    verdict: "blocked",
    headline: true,
  },
  { host: "webhook.attacker.tld", what: "your .env, POSTed somewhere else", verdict: "blocked", headline: true },
  { host: "crypto-pool.example", what: "a miner the dependency chain brought along", verdict: "blocked" },
  { host: "telemetry.unknown-vendor.io", what: "phone-home nobody asked for", verdict: "blocked" },
];

export const VERDICT_COPY: Record<Verdict, { label: string; detail: string }> = {
  baseline: {
    label: "Baseline",
    detail: "Always permitted — agent APIs and package registries, so installs and git keep working.",
  },
  allowed: {
    label: "--allow",
    detail: "A name you added with --allow or in your config, within what the server's policy lets a request add.",
  },
  blocked: {
    label: "Denied",
    detail: "Default-deny. The guest's DNS does not resolve it, and a connection to its address is refused on the host.",
  },
};
