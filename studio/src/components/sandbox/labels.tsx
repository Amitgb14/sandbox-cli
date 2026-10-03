import { Badge } from "@/components/ui/badge";

/** A sandbox's labels, as chips. Text from a client: rendered, never interpreted. */
export function Labels({ labels }: { labels?: Record<string, string> }) {
  const entries = Object.entries(labels ?? {});
  if (entries.length === 0) return <span className="text-muted-foreground">—</span>;
  return (
    <div className="flex flex-wrap gap-1">
      {entries.map(([k, v]) => (
        <Badge key={k} variant="outline" className="font-mono text-[10px] font-normal">
          {k}={v}
        </Badge>
      ))}
    </div>
  );
}
