"use client";

import { useRef, type ReactNode } from "react";
import { cn } from "@/lib/utils";

/** Cursor-following highlight. Pointer-only; no-ops for touch and reduced motion. */
export function Spotlight({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  const ref = useRef<HTMLDivElement | null>(null);

  return (
    <div
      ref={ref}
      onPointerMove={(e) => {
        const el = ref.current;
        if (!el || e.pointerType !== "mouse") return;
        const r = el.getBoundingClientRect();
        el.style.setProperty("--mx", `${e.clientX - r.left}px`);
        el.style.setProperty("--my", `${e.clientY - r.top}px`);
      }}
      className={cn("group relative overflow-hidden", className)}
      style={{ ["--mx" as string]: "50%", ["--my" as string]: "50%" }}
    >
      <span
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 opacity-0 transition-opacity duration-300 group-hover:opacity-100 motion-reduce:hidden"
        style={{
          background:
            "radial-gradient(220px circle at var(--mx) var(--my), color-mix(in oklch, var(--contained) 13%, transparent), transparent 65%)",
        }}
      />
      <span className="relative flex h-full flex-col gap-2">{children}</span>
    </div>
  );
}
