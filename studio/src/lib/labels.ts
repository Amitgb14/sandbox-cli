/**
 * Labels as people type and read them: `key=value`, or a bare `key` for a
 * label used as a tag. The gateway checks them the same way (spec.ValidateLabels);
 * checking here too says what is wrong as it is typed, not after a submit.
 */

const KEY = /^[a-z0-9][a-z0-9._/-]{0,62}$/;
export const MAX_LABELS = 32;
const MAX_VALUE_BYTES = 256;

/** One label as a chip reads: `k=v`, or `k` when its value is empty. */
export function labelTag(k: string, v: string): string {
  return v ? `${k}=${v}` : k;
}

export function labelTags(labels?: Record<string, string>): string[] {
  return Object.entries(labels ?? {}).map(([k, v]) => labelTag(k, v));
}

/** `key=value` or `key` into its parts. */
export function parseLabel(entry: string): [string, string] {
  const i = entry.indexOf("=");
  return i < 0 ? [entry, ""] : [entry.slice(0, i), entry.slice(i + 1)];
}

/** Why the gateway would refuse this label, or null. */
export function labelProblem(entry: string): string | null {
  const [k, v] = parseLabel(entry);
  if (!KEY.test(k)) return "Keys are lowercase letters, digits and . _ / -, at most 63, starting with a letter or digit";
  if (new TextEncoder().encode(v).length > MAX_VALUE_BYTES) return `Values are at most ${MAX_VALUE_BYTES} bytes`;
  if (/[\u0000-\u001f\u007f-\u009f]/.test(v)) return "Values cannot hold control characters";
  return null;
}

/** Entries as a label map; a key typed twice keeps its last value. */
export function labelMap(entries: string[]): Record<string, string> {
  return Object.fromEntries(entries.map(parseLabel));
}
