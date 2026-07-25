"use client";

import { useEffect, useRef, useState } from "react";
import { Check, Copy } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export function CopyButton({
  value,
  label,
  className,
  size = "sm",
}: {
  value: string;
  label?: string;
  className?: string;
  size?: "xs" | "sm" | "default";
}) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => () => clearTimeout(timer.current), []);

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
    } catch {
      return; // clipboard blocked — say nothing rather than lie about it
    }
    setCopied(true);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopied(false), 1800);
  }

  return (
    <Button
      type="button"
      variant="ghost"
      size={label ? size : size === "default" ? "icon" : `icon-${size}`}
      onClick={copy}
      aria-label={copied ? "Copied" : `Copy ${label ?? "to clipboard"}`}
      className={cn("shrink-0 text-muted-foreground hover:text-foreground", className)}
    >
      {copied ? <Check className="text-contained" /> : <Copy />}
      {label ? <span className="tnum">{copied ? "Copied" : label}</span> : null}
    </Button>
  );
}
