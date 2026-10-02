"use client";

import { FolderGit2 } from "lucide-react";
import { EmptyState } from "@/components/common/empty-state";
import { useRepos } from "@/lib/api/queries";
import { useUi } from "@/lib/store";
import type { Repo } from "@/lib/types";

/**
 * The work screens are about one repository. With none chosen they say how to
 * get one, rather than rendering a form or a table about nothing.
 */
export function RepoGate({ children }: { children: (repo: Repo) => React.ReactNode }) {
  const { data: repos, isLoading } = useRepos();
  const id = useUi((s) => s.repo);
  const repo = repos?.find((r) => r.id === id && !r.missing);
  if (isLoading) return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (!repo)
    return (
      <EmptyState
        icon={FolderGit2}
        title="No repository chosen"
        description="Run sandbox-cli studio from a git checkout, or add one from the repository menu at the top of the sidebar."
      />
    );
  return <>{children(repo)}</>;
}
