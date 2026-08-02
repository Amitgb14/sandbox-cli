import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";

/**
 * The frame every chart sits in.
 *
 * The title *names the series* — which is why a single-series chart needs no
 * legend box, and a multi-series one still gets one. `aside` is for the range
 * control or a peak readout, never for a second legend.
 */
export function ChartCard({
  title,
  description,
  aside,
  children,
  className,
  contentClassName,
}: {
  title: React.ReactNode;
  description?: React.ReactNode;
  aside?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
  contentClassName?: string;
}) {
  return (
    <Card className={cn("surface-sheen gap-4", className)}>
      <CardHeader className="gap-1">
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="space-y-1">
            <CardTitle className="text-sm font-medium">{title}</CardTitle>
            {description && (
              <CardDescription className="text-xs">{description}</CardDescription>
            )}
          </div>
          {aside && <div className="shrink-0">{aside}</div>}
        </div>
      </CardHeader>
      <CardContent className={cn("pb-2", contentClassName)}>{children}</CardContent>
    </Card>
  );
}
