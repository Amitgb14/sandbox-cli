"use client";

import { useState } from "react";
import { Check, Copy } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

export function CopyButton({
  value,
  label = "Copy",
  className,
  size = "icon",
}: {
  value: string;
  label?: string;
  className?: string;
  size?: "icon" | "sm";
}) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1600);
    } catch {
      // A clipboard the browser refused is not worth a toast; the text is on
      // screen and selectable either way.
    }
  }

  const Icon = copied ? Check : Copy;

  if (size === "sm") {
    return (
      <Button variant="outline" size="sm" onClick={copy} className={cn("gap-1.5", className)}>
        <Icon className={cn("size-3.5", copied && "text-status-good")} />
        {copied ? "Copied" : label}
      </Button>
    );
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          onClick={copy}
          aria-label={label}
          className={cn("size-7 text-muted-foreground hover:text-foreground", className)}
        >
          <Icon className={cn("size-3.5", copied && "text-status-good")} />
        </Button>
      </TooltipTrigger>
      <TooltipContent>{copied ? "Copied" : label}</TooltipContent>
    </Tooltip>
  );
}
