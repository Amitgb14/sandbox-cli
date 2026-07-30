import { Suspense } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import { PageHeader } from "@/components/common/page-header";
import { LaunchForm } from "@/components/launch/launch-form";

export const metadata = { title: "Launch" };

export default function LaunchPage() {
  return (
    <div className="space-y-6">
      <PageHeader
        title="Launch a run"
        description="Every control that widens what the container can reach says so where it sits, and the preview recomputes as you type — so nothing here is a refusal you find out about by pressing the button."
      />
      {/* useSearchParams needs a boundary: the agent can be preselected by link
          (?agent=codex) from the palette and the agents page. */}
      <Suspense fallback={<Skeleton className="h-[32rem] w-full" />}>
        <LaunchForm />
      </Suspense>
    </div>
  );
}
