"use client";

import { useEffect, useState } from "react";
import { CheckCircle2, CircleSlash, XCircle } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useRouting, useSetProviders } from "@/lib/api/queries";

/**
 * One agent's probe host, editable where the agent is.
 *
 * The same setting as the Routing screen's list, in the place somebody is
 * already looking at that agent — and reading and writing the same daemon state,
 * not a second copy. A preference that could be set in two places and disagree
 * is the drift this codebase keeps refusing to accept elsewhere.
 *
 * What it sets is which host routing asks before choosing this agent. Blank
 * means "do not probe", which is honest for an agent whose provider this machine
 * cannot reach, and the default for a provider-agnostic one like opencode where
 * nothing true can be compiled in.
 */
export function ProviderField({ agent }: { agent: string }) {
  const { data: providers } = useRouting();
  const setProviders = useSetProviders();
  const status = providers?.find((p) => p.agent === agent);
  const [editing, setEditing] = useState(false);
  const [host, setHost] = useState("");

  useEffect(() => setHost(status?.host ?? ""), [status?.host]);

  if (!status) return null;

  function save(next: string) {
    setProviders.mutate({
      // The whole *managed* set with this edit applied: the endpoint writes a
      // map, so sending one key alone would forget the rest — and `managed`
      // rather than `overridden`, because a host from the user's config.yaml is
      // set but not ours to copy into Studio's file.
      ...Object.fromEntries(
        (providers ?? []).filter((p) => p.managed).map((p) => [p.agent, p.host ?? ""]),
      ),
      [agent]: next,
    });
    setEditing(false);
  }

  return (
    <div className="flex items-center gap-1.5 text-[11px]">
      <Dot status={status} />
      {editing ? (
        <>
          <Input
            autoFocus
            value={host}
            onChange={(e) => setHost(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") save(host.trim());
              if (e.key === "Escape") {
                setHost(status?.host ?? "");
                setEditing(false);
              }
            }}
            placeholder="api.groq.com — blank to stop probing"
            className="h-6 font-mono text-[11px]"
          />
          <Button size="sm" className="h-6 px-2 text-[10px]" onClick={() => save(host.trim())}>
            Save
          </Button>
        </>
      ) : (
        <button
          onClick={() => setEditing(true)}
          className="min-w-0 flex-1 truncate rounded px-1 text-left font-mono text-muted-foreground hover:bg-accent"
          title="The host routing probes before choosing this agent"
        >
          {status.host || "no provider set"}
        </button>
      )}
    </div>
  );
}

function Dot({ status }: { status: { probed: boolean; reachable: boolean; reason?: string } }) {
  // Three states, because "not checked" is its own answer: an agent with no host
  // has nothing to ask, and colouring that red would condemn a working agent on
  // a question nobody put.
  if (!status.probed) {
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <CircleSlash className="size-3 shrink-0 text-muted-foreground" />
        </TooltipTrigger>
        <TooltipContent className="max-w-xs">
          Not checked — no host is set, so routing cannot skip this agent before a run. It
          still works, and on the CLI a failed run still falls through.
        </TooltipContent>
      </Tooltip>
    );
  }
  if (status.reachable) {
    return <CheckCircle2 className="size-3 shrink-0 text-status-good" />;
  }
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <XCircle className="size-3 shrink-0 text-status-critical" />
      </TooltipTrigger>
      <TooltipContent className="max-w-xs">
        {status.reason ?? "Not answering"} — from this machine, which is also what a proxy
        or an offline laptop looks like.
      </TooltipContent>
    </Tooltip>
  );
}
