import { formatArgv, shellQuote } from "@/lib/format";
import type { NetworkMode, VolumeMount } from "@/lib/types";

/**
 * The same sandbox, written for each client: the CLI, the HTTP API, and the
 * two SDKs. The Playground shows these beside its form, so a run set up by
 * hand can be repeated from a script without guessing at flag names.
 *
 * Every line here must be exactly what the client accepts: a snippet that
 * almost works teaches the wrong call. So it covers what all four can say —
 * a command, its image or snapshot, its snapshot schedule, size, name,
 * network, allow and deny lists, labels, volumes and a metrics export. A
 * header's value is never written into a snippet: the CLI's flag names an
 * environment variable to read it from, and an API body holds a placeholder
 * where it goes. An agent run is
 * the CLI's alone (the agent's login is copied in by sandbox-cli, not by the
 * API), so for one the code tabs show only the CLI.
 */
export interface RunConfig {
  command: string[];
  /** An image instead of the server's default (--image; "image"). */
  image?: string;
  /** A snapshot to start from instead of an image (--from-snapshot; "snapshot_id"). */
  snapshot?: string;
  /** A snapshot schedule (--snapshot-every/--snapshot-keep; "snapshot_every_secs"/"snapshot_keep"). */
  snapshotEverySecs?: number;
  snapshotKeep?: number;
  /** "suspend" has the idle timeout suspend it rather than terminate it (--on-idle; "idle_action"). */
  idleAction?: "suspend";
  /** A size (--cpus/--memory/--disk; "cpus"/"memory_mb"/"disk_mb"); absent is the server's default. */
  cpus?: number;
  memoryMb?: number;
  diskMb?: number;
  /** Whole GPUs (--gpus; "gpus"). */
  gpus?: number;
  name?: string;
  network?: "" | NetworkMode;
  allow?: string[];
  /** Hosts refused even where allowed (--deny; network "deny"). */
  deny?: string[];
  /** An allowlist of the named hosts only, without the built-in ones (--no-baseline). */
  noBaseline?: boolean;
  labels?: Record<string, string>;
  volumes?: VolumeMount[];
  /** A metrics export (--metrics-export…; "metrics_export"): header names only, never values. */
  metricsExport?: ExportConfig;
  /**
   * What `--allow` on the CLI adds to: the server's default allowlist, or the
   * built-in baseline when the server's default is not an allowlist. The
   * API's `allow` replaces rather than adds, so an API snippet spells the list
   * out whole — otherwise the same run is stricter from code than from the
   * terminal.
   */
  defaultAllow?: string[];
}

export interface ExportConfig {
  endpoint: string;
  intervalSecs?: number;
  /** Headers typed in: their names, the values left out. */
  headers?: string[];
  /** Headers from a gateway secret: header name to secret name. */
  headerSecrets?: Record<string, string>;
}

/** The environment variable a CLI snippet reads a header's value from: Authorization is OTEL_AUTHORIZATION. */
export function headerEnv(name: string): string {
  return "OTEL_" + name.toUpperCase().replace(/[^A-Z0-9]/g, "_");
}

/** Seconds as a Go duration the CLI's flags take: 30m, 6h, 90s. */
export function goDuration(secs: number): string {
  if (secs % 3600 === 0) return `${secs / 3600}h`;
  if (secs % 60 === 0) return `${secs / 60}m`;
  return `${secs}s`;
}

/** The address a snippet points at: Studio proxies, so the real one is the user's. */
export const ENDPOINT_PLACEHOLDER = "https://sandboxd.example:7443";

function cliFlags(c: Omit<RunConfig, "command" | "defaultAllow">): string[] {
  const f: string[] = [];
  if (c.snapshot) f.push("--from-snapshot", c.snapshot);
  else if (c.image) f.push("--image", c.image);
  if (c.snapshotEverySecs) {
    f.push("--snapshot-every", goDuration(c.snapshotEverySecs));
    if (c.snapshotKeep) f.push("--snapshot-keep", String(c.snapshotKeep));
  }
  if (c.idleAction) f.push("--on-idle", c.idleAction);
  if (c.cpus) f.push("--cpus", String(c.cpus));
  if (c.memoryMb) f.push("--memory", String(c.memoryMb));
  if (c.diskMb) f.push("--disk", String(c.diskMb));
  if (c.gpus) f.push("--gpus", String(c.gpus));
  if (c.name) f.push("--name", c.name);
  if (c.network) f.push("--network", c.network);
  for (const a of c.allow ?? []) f.push("--allow", a);
  for (const d of c.deny ?? []) f.push("--deny", d);
  if (c.noBaseline) f.push("--no-baseline");
  for (const [k, v] of Object.entries(c.labels ?? {})) f.push("--label", `${k}=${v}`);
  for (const v of c.volumes ?? []) f.push("--volume", `${v.name}:${v.path}${v.read_only ? ":ro" : ""}`);
  if (c.metricsExport?.endpoint) {
    const me = c.metricsExport;
    f.push("--metrics-export", me.endpoint);
    if (me.intervalSecs && me.intervalSecs !== 60) f.push("--metrics-export-every", goDuration(me.intervalSecs));
    for (const h of me.headers ?? []) f.push("--metrics-export-header", `${h}=${headerEnv(h)}`);
    for (const [h, sec] of Object.entries(me.headerSecrets ?? {})) f.push("--metrics-export-header-secret", `${h}=${sec}`);
  }
  return f;
}

export function cliRun(c: RunConfig): string {
  return formatArgv(["sandbox-cli", "run", ...cliFlags(c), "--", ...(c.command.length ? c.command : ["COMMAND"])]);
}

export function cliAgent(agent: string, c: Omit<RunConfig, "command" | "defaultAllow">, prompt?: string): string {
  const base = formatArgv(["sandbox-cli", "agent", agent, ...cliFlags(c)]);
  // A console run's first turn is the agent's own argument; everything after
  // the sandbox flags goes to the agent untouched.
  return prompt ? `${base} -- ${shellQuote(prompt)}` : base;
}

/** The create request body every API client sends. */
function createBody(c: Omit<RunConfig, "command">): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  if (c.snapshot) body.snapshot_id = c.snapshot;
  else if (c.image) body.image = c.image;
  if (c.snapshotEverySecs) {
    body.snapshot_every_secs = c.snapshotEverySecs;
    if (c.snapshotKeep) body.snapshot_keep = c.snapshotKeep;
  }
  if (c.idleAction) body.idle_action = c.idleAction;
  if (c.cpus) body.cpus = c.cpus;
  if (c.memoryMb) body.memory_mb = c.memoryMb;
  if (c.diskMb) body.disk_mb = c.diskMb;
  if (c.gpus) body.gpus = c.gpus;
  if (c.name) body.name = c.name;
  // The API's allow is the whole list: the baseline is spelled out unless the
  // run leaves it out.
  const allow = c.allow?.length ? [...new Set([...(c.noBaseline ? [] : (c.defaultAllow ?? [])), ...c.allow])] : undefined;
  // A deny list means nothing to a run that reaches nothing.
  const deny = c.network !== "none" && c.deny?.length ? { deny: c.deny } : {};
  if (c.network === "allowlist" || (!c.network && allow)) body.network = { mode: "allowlist", ...(allow ? { allow } : {}), ...deny };
  else if (c.network) body.network = { mode: c.network, ...deny };
  else if (deny.deny) body.network = deny;
  if (c.labels && Object.keys(c.labels).length) body.labels = c.labels;
  if (c.volumes?.length) body.volumes = c.volumes.map((v) => (v.read_only ? v : { name: v.name, path: v.path }));
  if (c.metricsExport?.endpoint) {
    const me = c.metricsExport;
    const out: Record<string, unknown> = { endpoint: me.endpoint };
    if (me.intervalSecs) out.interval_secs = me.intervalSecs;
    if (me.headers?.length) out.headers = Object.fromEntries(me.headers.map((h) => [h, `<${h} value>`]));
    if (me.headerSecrets && Object.keys(me.headerSecrets).length) out.header_secrets = me.headerSecrets;
    body.metrics_export = out;
  }
  return body;
}

export function curlRun(c: RunConfig): string {
  const body = JSON.stringify(createBody(c));
  const argv = JSON.stringify({ argv: c.command.length ? c.command : ["COMMAND"] });
  return [
    `SBX=${ENDPOINT_PLACEHOLDER}; AUTH="Authorization: Bearer $SANDBOX_TOKEN"`,
    `ID=$(curl -fsS -H "$AUTH" -H 'Content-Type: application/json' \\`,
    `  -d ${shellQuote(body)} $SBX/v1/sandboxes | jq -r .id)`,
    `curl -fsS -H "$AUTH" -H 'Content-Type: application/json' \\`,
    `  -d ${shellQuote(argv)} $SBX/v1/sandboxes/$ID/run`,
    `curl -fsS -X DELETE -H "$AUTH" $SBX/v1/sandboxes/$ID`,
  ].join("\n");
}

function pyValue(v: unknown): string {
  if (v === true) return "True";
  if (v === false) return "False";
  if (v === null || v === undefined) return "None";
  if (typeof v === "string") return JSON.stringify(v);
  if (typeof v === "number") return String(v);
  if (Array.isArray(v)) return `[${v.map(pyValue).join(", ")}]`;
  return `{${Object.entries(v as Record<string, unknown>).map(([k, x]) => `${JSON.stringify(k)}: ${pyValue(x)}`).join(", ")}}`;
}

export function pythonRun(c: RunConfig): string {
  const args = Object.entries(createBody(c)).map(([k, v]) => `${k}=${pyValue(v)}`);
  return [
    "import os",
    "from sandboxapi import Client",
    "",
    `c = Client(${JSON.stringify(ENDPOINT_PLACEHOLDER)}, token=os.environ["SANDBOX_TOKEN"])`,
    `sb = c.create_sandbox(${args.join(", ")})`,
    "try:",
    `    res = c.run(sb["id"], ${pyValue(c.command.length ? c.command : ["COMMAND"])})`,
    `    print(res["stdout"].decode(), end="")`,
    "finally:",
    `    c.terminate_sandbox(sb["id"])`,
  ].join("\n");
}

export function typescriptRun(c: RunConfig): string {
  const body = JSON.stringify(createBody(c));
  return [
    `import { Client } from "sandboxapi";`,
    "",
    `const c = new Client(${JSON.stringify(ENDPOINT_PLACEHOLDER)}, { token: process.env.SANDBOX_TOKEN });`,
    `const sb = await c.createSandbox(${body === "{}" ? "" : body});`,
    "try {",
    `  const res = await c.run(sb.id, ${JSON.stringify(c.command.length ? c.command : ["COMMAND"])});`,
    "  process.stdout.write(new TextDecoder().decode(res.stdout));",
    "} finally {",
    "  await c.terminateSandbox(sb.id);",
    "}",
  ].join("\n");
}

/** The four snippets for a command run, in the order the tabs show them. */
export function snippets(c: RunConfig): { id: string; label: string; code: string }[] {
  return [
    { id: "cli", label: "CLI", code: cliRun(c) },
    { id: "curl", label: "curl", code: curlRun(c) },
    { id: "python", label: "Python", code: pythonRun(c) },
    { id: "typescript", label: "TypeScript", code: typescriptRun(c) },
  ];
}
