import Link from "next/link";
import { Compass } from "lucide-react";
import { Button } from "@/components/ui/button";

export default function NotFound() {
  return (
    <div className="flex min-h-svh items-center justify-center p-6">
      <div className="max-w-md space-y-4 text-center">
        <div className="mx-auto flex size-11 items-center justify-center rounded-full bg-muted">
          <Compass className="size-5 text-muted-foreground" />
        </div>
        <div className="space-y-1.5">
          <h1 className="text-lg font-semibold">No such screen</h1>
          <p className="text-sm text-muted-foreground">
            Nothing in Studio answers that path. The command palette (⌘K) reaches everything there
            is.
          </p>
        </div>
        <Button asChild size="sm">
          <Link href="/">Back to the dashboard</Link>
        </Button>
      </div>
    </div>
  );
}
