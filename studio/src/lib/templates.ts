import { formatMiB } from "@/lib/format";
import type { Limits, VmTemplate } from "@/lib/types";

/** What of a template this endpoint would refuse, said plainly; "" when it fits. */
export function overLimits(t: Pick<VmTemplate, "cpus" | "memory_mb" | "disk_mb">, l?: Limits): string {
  if (!l) return "";
  const over: string[] = [];
  if (t.cpus > l.max_cpus) over.push(`${t.cpus} vCPUs (max ${l.max_cpus})`);
  if (t.memory_mb > l.max_memory_mb) over.push(`${formatMiB(t.memory_mb)} (max ${formatMiB(l.max_memory_mb)})`);
  if (t.disk_mb && t.disk_mb > l.max_disk_mb) over.push(`${formatMiB(t.disk_mb)} disk (max ${formatMiB(l.max_disk_mb)})`);
  return over.length ? `Above this endpoint's limits: ${over.join(", ")}` : "";
}
