import type { ServiceSpec } from "@/lib/types";

/**
 * What is wrong with a service spec's command before it is sent, or null.
 * A command is an argv: its first item is the program. ["sleep 40"] asks for
 * a program named "sleep 40", which no image has, and the gateway finds that
 * only when the first replica fails to start. A one-item command holding a
 * space is nearly always a shell line, so it is refused here with the two
 * spellings that mean it.
 */
export function commandProblem(spec: ServiceSpec): string | null {
  const c = spec.command;
  if (c === undefined) return null;
  if (!Array.isArray(c) || c.some((a) => typeof a !== "string")) return 'command is a list of strings: ["sleep", "infinity"]';
  if (c.length === 1 && /\s/.test(c[0].trim())) {
    const words = c[0].trim().split(/\s+/);
    return `command ${JSON.stringify(c)} names one program, "${c[0]}". Split it into the program and its arguments, ${JSON.stringify(words)}, or run it as a shell line, ${JSON.stringify(["sh", "-c", c[0]])}.`;
  }
  return null;
}
