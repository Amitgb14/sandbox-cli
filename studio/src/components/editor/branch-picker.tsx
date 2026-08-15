"use client";

import { GitBranch } from "lucide-react";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Badge } from "@/components/ui/badge";
import { useWorktrees } from "@/lib/api/queries";

/**
 * Which branch the Editor screens are about.
 *
 * The empty value is the repository's **own checkout**, and it is a real choice
 * rather than a placeholder: that directory is where a run launched without
 * `--worktree` works, and it is the one branch with no worktree of its own. The
 * rest of the list is worktrees, each a separate directory on disk — which is
 * why "show me this branch's files" is answered by browsing a different
 * directory rather than by asking git to render a tree at a ref.
 *
 * Dirty counts are shown because they are the reason to look: a worktree with
 * uncommitted files is one where an agent left work that is not in any commit.
 */
export function BranchPicker({
  value,
  onChange,
  className,
  /** Hide the repository's own checkout, for a screen that only means worktrees. */
  worktreesOnly,
}: {
  value: string;
  onChange: (branch: string) => void;
  className?: string;
  worktreesOnly?: boolean;
}) {
  const { data: worktrees } = useWorktrees();
  // The repository's own checkout, which the daemon marks rather than leaving
  // for a client to guess at. It is named by the branch it actually has — the
  // option used to read "Its own checkout", which meant the branch you are on
  // (usually `main`) appeared nowhere and the picker never showed where you
  // were standing.
  const primary = (worktrees ?? []).find((w) => w.primary);
  const branches = (worktrees ?? []).filter((w) => !w.primary);

  return (
    <Select value={value} onValueChange={(v) => onChange(v === CHECKOUT ? "" : v)}>
      <SelectTrigger className={className}>
        <GitBranch className="size-3.5 text-muted-foreground" />
        <SelectValue placeholder="Pick a branch" />
      </SelectTrigger>
      <SelectContent>
        {!worktreesOnly && (
          <SelectGroup>
            <SelectLabel className="text-xs">The repository&apos;s own checkout</SelectLabel>
            <SelectItem value={CHECKOUT}>
              <span className="font-mono text-xs">{primary?.branch ?? "checkout"}</span>
              {primary && primary.dirty.length > 0 && (
                <Badge variant="outline" className="ml-1.5 text-[10px]">
                  {primary.dirty.length} dirty
                </Badge>
              )}
            </SelectItem>
          </SelectGroup>
        )}
        <SelectGroup>
          <SelectLabel className="text-xs">Worktrees</SelectLabel>
          {branches.map((w) => (
            <SelectItem key={w.branch} value={w.branch}>
              <span className="font-mono text-xs">{w.branch}</span>
              {w.dirty.length > 0 && (
                <Badge variant="outline" className="ml-1.5 text-[10px]">
                  {w.dirty.length} dirty
                </Badge>
              )}
            </SelectItem>
          ))}
          {branches.length === 0 && (
            <p className="px-2 py-1.5 text-xs text-muted-foreground">
              No worktrees yet — launch a run with one, or add it from Worktrees.
            </p>
          )}
        </SelectGroup>
      </SelectContent>
    </Select>
  );
}

/**
 * The select's stand-in for "no branch".
 *
 * Radix's Select refuses an empty string as an item value — it uses "" for the
 * cleared state — so the one option whose real value *is* empty needs a token,
 * translated at both ends rather than leaked to callers.
 */
export const CHECKOUT = "__checkout__";

/** The value a picker should show for a branch, mapping "" to the token. */
export function branchValue(branch: string): string {
  return branch === "" ? CHECKOUT : branch;
}
