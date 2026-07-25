/**
 * Traffic the egress visualiser sends at the firewall. Verdicts follow the real
 * rule from README.md: with `network: allowlist` (or --allow), outbound is
 * default-deny except DNS, established flows, a baseline of agent APIs and
 * package registries, and the domains you add.
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
    what: "your private registry — added with --allow",
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
    detail: "A domain you named on the command line or in .sandbox.yaml.",
  },
  blocked: {
    label: "Denied",
    detail: "Default-deny. Nothing else leaves the container.",
  },
};
