"use client";

import { useState } from "react";
import { X } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

/**
 * A list of short strings — domains, env var names, paths.
 *
 * Commit on Enter or comma, remove with Backspace on an empty field. `invalid`
 * lets the caller mark an entry that will be refused (a reserved environment
 * variable, say) without removing it: silently dropping what someone typed is
 * how they end up debugging a setting that never applied.
 */
export function TagInput({
  value,
  onChange,
  placeholder,
  invalid,
  className,
  id,
}: {
  value: string[];
  onChange: (next: string[]) => void;
  placeholder?: string;
  /** Returns a reason when an entry will be refused, or null when it is fine. */
  invalid?: (entry: string) => string | null;
  className?: string;
  id?: string;
}) {
  const [draft, setDraft] = useState("");

  function commit(raw: string) {
    const parts = raw
      .split(/[\s,]+/)
      .map((p) => p.trim())
      .filter(Boolean);
    if (parts.length === 0) return;
    const next = [...value];
    for (const p of parts) if (!next.includes(p)) next.push(p);
    onChange(next);
    setDraft("");
  }

  return (
    <div className={cn("space-y-2", className)}>
      <Input
        id={id}
        value={draft}
        placeholder={placeholder}
        onChange={(e) => {
          const v = e.target.value;
          if (v.endsWith(",")) commit(v.slice(0, -1));
          else setDraft(v);
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            commit(draft);
          } else if (e.key === "Backspace" && draft === "" && value.length > 0) {
            onChange(value.slice(0, -1));
          }
        }}
        onBlur={() => commit(draft)}
      />
      {value.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {value.map((entry) => {
            const reason = invalid?.(entry) ?? null;
            return (
              <Badge
                key={entry}
                variant="outline"
                title={reason ?? undefined}
                className={cn(
                  "gap-1 pr-1 font-mono text-[11px]",
                  reason && "border-destructive/50 bg-destructive/10 text-destructive",
                )}
              >
                {entry}
                <button
                  type="button"
                  onClick={() => onChange(value.filter((v) => v !== entry))}
                  className="rounded-sm opacity-60 hover:opacity-100"
                  aria-label={`Remove ${entry}`}
                >
                  <X className="size-3" />
                </button>
              </Badge>
            );
          })}
        </div>
      )}
    </div>
  );
}
