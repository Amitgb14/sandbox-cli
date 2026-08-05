import { UNINSTALL_STEPS } from "@/lib/setup";
import { CopyButton } from "@/components/copy-button";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

/**
 * Uninstall, shown rather than described.
 *
 * Deliberately the same card shape as SetupGuide's steps: this is the reverse
 * of that list and reading it should feel like reading that one. No tabs — the
 * commands are identical on every platform the installer covers, and a tab
 * strip with one option in it is a control that only asks a question.
 */
export function UninstallSteps({ className }: { className?: string }) {
  return (
    <ol className={cn("flex flex-col gap-3", className)}>
      {UNINSTALL_STEPS.map((s, i) => (
        <li key={s.title} className="rounded-xl border bg-card px-4 py-3.5">
          <div className="flex items-baseline gap-2.5">
            <Badge
              variant="outline"
              className="shrink-0 border-border font-mono text-[0.62rem] font-normal text-muted-foreground"
            >
              {i + 1}
            </Badge>
            <h4 className="text-[0.88rem] font-medium">{s.title}</h4>
          </div>

          {s.code ? (
            <div className="group relative mt-2.5 overflow-x-auto rounded-lg border bg-muted/40 px-3.5 py-2.5">
              <pre className="font-mono text-[0.72rem] leading-relaxed whitespace-pre">
                {s.code}
              </pre>
              <div className="absolute top-2 right-2 opacity-0 transition-opacity group-hover:opacity-100">
                <CopyButton value={s.code} />
              </div>
            </div>
          ) : null}

          <p className="mt-2.5 text-[0.8rem] leading-relaxed text-muted-foreground">
            {s.body}
          </p>
        </li>
      ))}
    </ol>
  );
}
