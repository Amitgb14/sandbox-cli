import { Cpu, HardDrive, MemoryStick } from "lucide-react";
import { formatMiB } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { Sandbox } from "@/lib/types";

/**
 * What a sandbox was given, as three chips. Allocations, not usage: sandboxd
 * reports no live usage, so nothing here is a gauge.
 */
export function ResourceChips({ sb, className }: { sb: Pick<Sandbox, "cpus" | "memory_mb" | "disk_mb">; className?: string }) {
  const chips = [
    { icon: Cpu, text: `${sb.cpus} vCPU`, title: "CPUs" },
    { icon: MemoryStick, text: formatMiB(sb.memory_mb), title: "Memory" },
    { icon: HardDrive, text: formatMiB(sb.disk_mb), title: "Disk" },
  ];
  return (
    <div className={cn("flex flex-wrap gap-1.5", className)}>
      {chips.map(({ icon: Icon, text, title }) => (
        <span
          key={title}
          title={title}
          className="inline-flex items-center gap-1 rounded-md border bg-muted/30 px-1.5 py-0.5 font-mono text-[11px] whitespace-nowrap tabular-nums"
        >
          <Icon aria-hidden className="size-3 text-muted-foreground" />
          {text}
        </span>
      ))}
    </div>
  );
}
