import { Check, CircleDot, Minus, X } from "lucide-react";
import { PLATFORMS } from "@/lib/comparison";
import { cn } from "@/lib/utils";

const OS = [
  { key: "macos", label: "macOS", sub: "Docker Desktop" },
  { key: "linux", label: "Linux", sub: "native Docker" },
  { key: "windows", label: "Windows", sub: "Docker Desktop / WSL2" },
] as const;

function Mark({ value }: { value: string }) {
  if (value === "yes")
    return (
      <span className="relative inline-flex items-center gap-1.5 text-contained">
        <Check className="size-3.5" />
        <span className="sr-only">supported</span>
      </span>
    );
  if (value === "no")
    return (
      <span className="relative inline-flex items-center gap-1.5 text-exposed">
        <X className="size-3.5" />
        <span className="sr-only">not available</span>
      </span>
    );
  if (value === "partial")
    return (
      <span className="inline-flex items-center gap-1.5 text-caution">
        <CircleDot className="size-3.5" />
        <span className="text-[0.7rem]">partly verified</span>
      </span>
    );
  return (
    <span className="inline-flex items-center gap-1.5 text-muted-foreground">
      <Minus className="size-3.5" />
      <span className="text-[0.7rem]">{value}</span>
    </span>
  );
}

export function PlatformTable({ className }: { className?: string }) {
  return (
    <div className={cn("overflow-hidden rounded-2xl border bg-card", className)}>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[42rem] border-collapse text-left">
          <thead>
            <tr className="border-b">
              <th className="px-4 py-3 text-[0.8rem] font-medium sm:px-5">Capability</th>
              {OS.map((o) => (
                <th key={o.key} className="px-4 py-3">
                  <span className="flex flex-col">
                    <span className="text-[0.8rem] font-medium">{o.label}</span>
                    <span className="text-[0.68rem] font-normal text-muted-foreground">
                      {o.sub}
                    </span>
                  </span>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {PLATFORMS.map((p) => (
              <tr key={p.capability} className="border-b last:border-0">
                <td className="px-4 py-3 align-top sm:px-5">
                  <span className="block text-[0.8rem]">{p.capability}</span>
                  {"footnote" in p && p.footnote ? (
                    <span className="mt-1 block max-w-md text-[0.7rem] leading-relaxed text-muted-foreground">
                      {p.footnote}
                    </span>
                  ) : null}
                </td>
                {OS.map((o) => (
                  <td key={o.key} className="px-4 py-3 align-top">
                    <Mark value={p[o.key]} />
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
