/**
 * Formatting helpers.
 *
 * The rule they all share: **absent is not zero.** A run with no memory limit,
 * a usage window whose cached figure is past its reset, a container that was
 * never sampled — each prints an em dash rather than `0`, the same bargain the
 * CLI makes with a transcript whose shape it no longer recognises.
 */

export const DASH = "—";

export function formatBytes(bytes: number | null | undefined, digits = 1): string {
  if (bytes === null || bytes === undefined || !Number.isFinite(bytes)) return DASH;
  if (bytes === 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  const i = Math.min(Math.floor(Math.log(Math.abs(bytes)) / Math.log(1024)), units.length - 1);
  const v = bytes / 1024 ** i;
  return `${v.toFixed(i === 0 ? 0 : digits)} ${units[i]}`;
}

/** Compact bytes for axis ticks and dense table cells: `1.4G`, `812M`. */
export function formatBytesShort(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined || !Number.isFinite(bytes)) return DASH;
  if (bytes === 0) return "0";
  const units = ["B", "K", "M", "G", "T"];
  const i = Math.min(Math.floor(Math.log(Math.abs(bytes)) / Math.log(1024)), units.length - 1);
  const v = bytes / 1024 ** i;
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)}${units[i]}`;
}

export function formatPercent(v: number | null | undefined, digits = 0): string {
  if (v === null || v === undefined || !Number.isFinite(v)) return DASH;
  return `${v.toFixed(digits)}%`;
}

/** `2h14m`, `4m 12s`, `812ms` — the CLI's own duration vocabulary. */
export function formatDuration(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || !Number.isFinite(ms)) return DASH;
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}

/** The tight form used inside badges and gauges: `2h14m`, `48s`. */
export function formatDurationTight(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || !Number.isFinite(ms)) return DASH;
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h${String(m % 60).padStart(2, "0")}m`;
  return `${Math.floor(h / 24)}d`;
}

/**
 * `4m ago`, `in 2h`. Deliberately not a live-ticking clock: a table of thirty
 * rows re-rendering every second to move one character is a cost with no reader.
 */
export function formatRelative(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return DASH;
  const t = new Date(iso).getTime();
  if (!Number.isFinite(t)) return DASH;
  const diff = t - now;
  const abs = Math.abs(diff);
  const suffix = diff < 0 ? " ago" : "";
  const prefix = diff > 0 ? "in " : "";
  const body =
    abs < 45_000
      ? "just now"
      : abs < 3_600_000
        ? `${Math.round(abs / 60_000)}m`
        : abs < 86_400_000
          ? `${Math.round(abs / 3_600_000)}h`
          : `${Math.round(abs / 86_400_000)}d`;
  return body === "just now" ? body : `${prefix}${body}${suffix}`;
}

export function formatClock(iso: string | null | undefined): string {
  if (!iso) return DASH;
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return DASH;
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return DASH;
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return DASH;
  return d.toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** Abbreviate a container id for display. Never for addressing. */
export function shortId(id: string, len = 12): string {
  return id.length <= len ? id : id.slice(0, len);
}

/**
 * Quote an argv element the way a shell would need it, so a copied command
 * actually runs. Used by every argv preview in the app.
 */
export function shellQuote(arg: string): string {
  if (arg === "") return "''";
  if (/^[A-Za-z0-9_@%+=:,./-]+$/.test(arg)) return arg;
  return `'${arg.replace(/'/g, `'\\''`)}'`;
}

export function formatArgv(argv: string[]): string {
  return argv.map(shellQuote).join(" ");
}

/** A path with the user's home collapsed, as every CLI output does. */
export function tildify(path: string, home = "/Users/amitghadge"): string {
  return path.startsWith(home) ? `~${path.slice(home.length)}` : path;
}

/** The last two segments of a path — enough to recognise, short enough to fit. */
export function basename(path: string, segments = 1): string {
  const parts = path.replace(/\/+$/, "").split("/").filter(Boolean);
  return parts.slice(-segments).join("/") || "/";
}

export function pluralize(n: number, one: string, many = `${one}s`): string {
  return `${n} ${n === 1 ? one : many}`;
}
