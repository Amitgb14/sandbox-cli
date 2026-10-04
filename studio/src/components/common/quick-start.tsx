"use client";

import Link from "next/link";
import { Boxes, Play } from "lucide-react";
import { CodeTabs } from "@/components/common/code-tabs";
import { Button } from "@/components/ui/button";
import { snippets } from "@/lib/codegen";

/**
 * The empty state for "no sandboxes": not a dead end but the first one, from
 * whichever client the reader uses. The snippet is the codegen's, so it is the
 * same code the Playground would show for this command.
 */
export function QuickStart({ title = "No sandboxes yet" }: { title?: string }) {
  return (
    <div className="grid items-center gap-6 rounded-xl border border-dashed p-6 md:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)]">
      <div className="flex flex-col items-start gap-3">
        <div className="flex size-10 items-center justify-center rounded-full bg-muted/60">
          <Boxes className="size-5 text-muted-foreground" aria-hidden />
        </div>
        <div className="space-y-1">
          <p className="text-sm font-medium">{title}</p>
          <p className="text-sm text-muted-foreground">
            Start one from the Playground, or from code. Each sandbox is a microVM with its own kernel that starts
            in its own home directory; every client below lands here.
          </p>
        </div>
        <Button asChild size="sm">
          <Link href="/launch">
            <Play className="size-4" />
            Open the Playground
          </Link>
        </Button>
      </div>
      <CodeTabs tabs={snippets({ command: ["uname", "-a"] })} />
    </div>
  );
}
