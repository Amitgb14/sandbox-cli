"use client";

import { Suspense } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { ArrowLeft } from "lucide-react";
import { SandboxDetails } from "@/components/sandbox/details";

/**
 * One sandbox as a page of its own, for a link to share or a tab to keep: the
 * same details as the list's panel, two panes on a wide screen.
 */
function SandboxDetail() {
  const id = useSearchParams().get("id") ?? "";
  const router = useRouter();
  if (!id) return <p className="text-sm text-muted-foreground">No sandbox named.</p>;
  return (
    <div className="flex flex-col gap-3">
      <Link href="/sandboxes" className="inline-flex w-fit items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft className="size-3.5" />
        Sandboxes
      </Link>
      <div className="overflow-hidden rounded-xl border bg-card lg:h-[calc(100svh-9rem)]">
        <SandboxDetails id={id} variant="page" onGone={() => router.push("/sandboxes")} onReplaced={(n) => router.replace(`/sandbox?id=${n}`)} />
      </div>
    </div>
  );
}

export default function SandboxPage() {
  return (
    <Suspense fallback={<p className="text-sm text-muted-foreground">Loading…</p>}>
      <SandboxDetail />
    </Suspense>
  );
}
