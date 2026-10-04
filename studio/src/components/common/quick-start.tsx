"use client";

import Link from "next/link";
import { Plus } from "lucide-react";
import { CodeTabs } from "@/components/common/code-tabs";
import { Button } from "@/components/ui/button";
import { snippets } from "@/lib/codegen";

/**
 * The empty state for "no sandboxes": not a dead end but the first one, from
 * the Playground or from whichever client the reader uses. The snippet is the
 * codegen's, so it is the same code the Playground would show.
 */
export function QuickStart() {
  const tabs = snippets({ command: ["uname", "-a"] });
  // Code first in the order people reach for it; the CLI last.
  const ordered = ["python", "typescript", "curl", "cli"].map((id) => tabs.find((t) => t.id === id)!).filter(Boolean);
  return (
    <div className="flex flex-col items-center gap-6 rounded-xl border border-dashed px-6 py-14 text-center">
      <div className="space-y-1.5">
        <p className="text-lg font-semibold tracking-tight">Spin up your first sandbox</p>
        <p className="text-sm text-muted-foreground">
          A microVM with its own kernel, starting in its own home directory.
        </p>
      </div>
      <div className="flex gap-2">
        <Button asChild size="sm">
          <Link href="/launch">
            <Plus className="size-4" />
            Create sandbox
          </Link>
        </Button>
        <Button asChild size="sm" variant="outline">
          <Link href="/launch">Open the Playground</Link>
        </Button>
      </div>
      <div className="flex w-full max-w-xl items-center gap-3 text-xs text-muted-foreground">
        <span className="h-px flex-1 bg-border" />
        or from code
        <span className="h-px flex-1 bg-border" />
      </div>
      <CodeTabs tabs={ordered} className="w-full max-w-xl text-left" />
    </div>
  );
}
