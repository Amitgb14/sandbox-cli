"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Bot, GitBranch, Play, ShieldCheck, Terminal } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Switch } from "@/components/ui/switch";
import { Separator } from "@/components/ui/separator";
import { Badge } from "@/components/ui/badge";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Skeleton } from "@/components/ui/skeleton";
import { TagInput } from "@/components/common/tag-input";
import { LaunchPreview } from "@/components/launch/launch-preview";
import {
  useAgentSessions,
  useAgents,
  useDaemon,
  useLaunchRun,
  useRemoveRun,
  useWorktrees,
} from "@/lib/api/queries";
import { localPreview } from "@/lib/api/endpoints";
import { formatRelative, pluralize } from "@/lib/format";
import { BASELINE_EGRESS, PROFILES, RESERVED_ENV } from "@/lib/constants";
import { REPOS } from "@/lib/mock/data";
import { useUi } from "@/lib/store";
import type {
  AgentName,
  LaunchRequest,
  NetworkMode,
  Profile,
  SessionSummary,} from "@/lib/types";
import { cn } from "@/lib/utils";

/**
 * Launch a run.
 *
 * The form's job is not only to collect settings — it is to make the boundary
 * legible *before* the container exists. Every control that widens what the
 * container can reach says so where it sits, and the preview beside it recomputes
 * on every keystroke, so nobody discovers a refusal by hitting submit.
 *
 * The refusals it shows are a documented subset. The daemon runs the real
 * `BuildSpec`, and a form that reimplemented the whole rule set would eventually
 * disagree with the thing that actually enforces it.
 */
export function LaunchForm() {
  const router = useRouter();
  const search = useSearchParams();
  const repoFilter = useUi((s) => s.repoFilter);
  const { data: agents } = useAgents();
  const { data: daemon } = useDaemon();
  const { data: worktrees } = useWorktrees();
  const launch = useLaunchRun();
  const removeRun = useRemoveRun();

  const initialAgent = (search.get("agent") as AgentName | null) ?? "claude";
  const initialRepo = REPOS.find((r) => r.id === repoFilter) ?? REPOS[0];

  const [req, setReq] = useState<LaunchRequest>({
    agent: initialAgent,
    command: "",
    prompt: "",
    workspace: initialRepo.root,
    worktree: null,
    base: "main",
    profile: "dev",
    network: { mode: "allowlist", baseline: true, allow: [] },
    memory: "4g",
    cpus: "2",
    detach: false,
    console: false,
    skipPermissions: false,
    resume: null,
    persistAuth: true,
    sync: true,
    statusline: true,
    verify: "",
    envAllow: [],
    share: [],
    publish: [],
  });

  // The daemon manages exactly one project, and it says which on /v1/health.
  // Adopt it as soon as it answers, unless you have already picked something
  // else: the initial value can only come from the fixture repos, and posting a
  // fixture path to a live daemon fails with "project path does not exist".
  useEffect(() => {
    if (!daemon?.project) return;
    setReq((prev) =>
      prev.workspace === initialRepo.root && !prev.worktree
        ? { ...prev, workspace: daemon.project as string }
        : prev,
    );
  }, [daemon?.project, initialRepo.root]);
  const [worktreeMode, setWorktreeMode] = useState<"main" | "new" | "existing">(
    "main",
  );
  const [newBranch, setNewBranch] = useState("");

  /**
   * `?branch=` arrives from the worktrees page's "Start an agent here" and from
   * the palette. It carries the branch alone, so the repository has to be looked
   * up from it — a branch is only meaningful inside the repo that holds it, and
   * selecting the branch without its repo would leave the picker empty.
   *
   * It fires once, on the first load of the worktree list rather than on mount,
   * because the list is fetched: applying it repeatedly would fight whatever the
   * person picked afterwards.
   */
  const deepLinkBranch = search.get("branch");
  const deepLinkApplied = useRef(false);

  useEffect(() => {
    if (deepLinkApplied.current || !deepLinkBranch || !worktrees) return;
    const match = worktrees.find(
      (w) => !w.primary && w.branch === deepLinkBranch,
    );
    if (!match) return;
    deepLinkApplied.current = true;
    // Deliberately does not touch `workspace`. It used to set it from REPOS,
    // which is fixture data — and the fixture's id matches the real repo id, so
    // the lookup *succeeded* and quietly replaced the daemon's project with a
    // path that exists on nobody's disk. The launch then failed with "project
    // path does not exist".
    //
    // A deep link carries a branch, and a branch is all it should apply. The
    // workspace already holds the daemon's own project, which is the only
    // project this server has.
    setReq((prev) => ({
      ...prev,
      worktree: match.branch,
      base: match.base ?? prev.base,
    }));
    setWorktreeMode("existing");
  }, [deepLinkBranch, worktrees]);

  function patch(next: Partial<LaunchRequest>) {
    setReq((prev) => ({ ...prev, ...next }));
  }

  const resolved: LaunchRequest = useMemo(
    () => ({
      ...req,
      worktree:
        worktreeMode === "main"
          ? null
          : worktreeMode === "new"
            ? newBranch || null
            : req.worktree,
    }),
    [req, worktreeMode, newBranch],
  );

  const preview = useMemo(() => localPreview(resolved), [resolved]);
  const blocked = preview.refusals.length > 0;

  const agentMeta = agents?.find((a) => a.name === req.agent);

  // An agent run with no console skips permissions whatever this form says —
  // *for the agents that have a flag to skip with*. `Descriptor.Autonomous`
  // appends `SkipPermissionArgs`, and that is empty for codex, opencode and
  // droid, whose non-interactive mode is a subcommand. Codex in particular
  // "applies its own approval policy on top" (its descriptor says so) and
  // sandbox-cli deliberately does not relax it, so claiming the run works
  // without asking would be the same false statement this control was fixed to
  // stop making, pointed the other way. A plain command is excluded too: no
  // agent, nothing to ask.
  const headlessAlwaysSkips =
    !!req.agent && !req.console && agentMeta?.canSkipPermissions === true;
  const skipFlag = agentMeta?.skipPermissionArgs?.join(" ");
  // Matched by repo **id**, never by a name derived from the workspace path.
  // Two clones of a same-named repo would otherwise share a namespace — and the
  // name-derived version was already wrong here, because a worktree path
  // carries the id (`intrupt-web-1f3ab902`) while the workspace directory
  // carries the name (`intrupt_web`), so it matched nothing for those repos.
  // One daemon manages one project. When it has answered, that project *is* the
  // repository list — and every worktree it reports belongs to it, so there is
  // nothing to filter by. REPOS is fixture data and only stands in before the
  // daemon replies; offering it against a live daemon is what put a path from
  // somebody's imagination into a launch request.
  const liveProject = daemon?.project ?? null;
  const repoOptions = liveProject
    ? [
        {
          id: "live",
          name: liveProject.split("/").filter(Boolean).pop() ?? liveProject,
          root: liveProject,
        },
      ]
    : REPOS;
  const selectedRepoId =
    repoOptions.find((r) => r.root === req.workspace)?.id ?? null;
  const repoWorktrees = (worktrees ?? []).filter(
    (w) => !w.primary && (liveProject !== null || w.repoId === selectedRepoId),
  );

  function submit() {
    launch.mutate(resolved, {
      onSuccess: ({ id }) => {
        toast.success("Sandbox starting", {
          description: resolved.detach
            ? "Detached — follow it from Runs, and its exit code is the whole supervision story."
            : "Attached. The terminal tab shows what it draws.",
        });
        router.push(`/runs/${id}`);
      },
      onError: (err) => {
        const message = err instanceof Error ? err.message : String(err);
        // A container name held by a *finished* run is the one failure here with
        // an obvious next step, and it used to end at a message telling you to
        // open a terminal. The daemon names the run in its refusal; offer to
        // reap it and try again from where you already are.
        //
        // Only for a finished one. "An agent is already running" carries a
        // similar message and must not get a button, because the fix there is a
        // decision about somebody's work in progress.
        const blocking = /a finished run \(([0-9a-f]+),/.exec(message)?.[1];
        toast.error("Could not start the run", {
          description: message,
          duration: blocking ? 15_000 : undefined,
          action: blocking
            ? {
                label: "Remove it and retry",
                onClick: () =>
                  removeRun.mutate(blocking, {
                    onSuccess: () => submit(),
                    onError: (e: unknown) =>
                      toast.error("Could not remove that run", {
                        description: e instanceof Error ? e.message : String(e),
                      }),
                  }),
              }
            : undefined,
        });
      },
    });
  }

  return (
    <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_28rem]">
      <div className="space-y-4">
        {/* ---------------------------------------------------------------- */}
        <Section icon={Bot} title="What to run">
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Agent" htmlFor="agent">
              <Select
                value={req.agent ?? "__none"}
                onValueChange={(v) =>
                  patch({ agent: v === "__none" ? null : (v as AgentName) })
                }
              >
                <SelectTrigger id="agent">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectLabel>
                      Verified headless — eligible for a fleet
                    </SelectLabel>
                    {agents
                      ?.filter((a) => a.headlessVerified)
                      .map((a) => (
                        <SelectItem key={a.name} value={a.name}>
                          {a.label}
                        </SelectItem>
                      ))}
                  </SelectGroup>
                  <SelectGroup>
                    <SelectLabel>Interactive only</SelectLabel>
                    {agents
                      ?.filter((a) => !a.headlessVerified)
                      .map((a) => (
                        <SelectItem key={a.name} value={a.name}>
                          {a.label}
                        </SelectItem>
                      ))}
                  </SelectGroup>
                  <SelectGroup>
                    <SelectLabel>No agent</SelectLabel>
                    <SelectItem value="__none">Plain command</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
              {agentMeta && !agentMeta.headlessVerified && req.detach && (
                <Hint tone="caution">
                  {agentMeta.label} has no verified headless argv. Detached, an
                  agent that stops to ask permission does not fail — it hangs.
                </Hint>
              )}
              {agentMeta && agentMeta.delivery !== "baked" && (
                <Hint>
                  Installed on first run into the persisted HOME (
                  {agentMeta.delivery}), so the image carries nothing for it.
                </Hint>
              )}
            </Field>

            {req.agent === null ? (
              <Field label="Command" htmlFor="command">
                <Input
                  id="command"
                  value={req.command}
                  onChange={(e) => patch({ command: e.target.value })}
                  placeholder="npm test"
                  className="font-mono"
                />
                <Hint>
                  Recorded verbatim in the run log — which is the known soft
                  edge in &ldquo;no secret values&rdquo;, and kept because a log
                  that cannot say what ran answers nothing.
                </Hint>
              </Field>
            ) : (
              <Field label="Resources" htmlFor="memory">
                <div className="grid grid-cols-2 gap-2">
                  <Input
                    id="memory"
                    value={req.memory}
                    onChange={(e) => patch({ memory: e.target.value })}
                    placeholder="4g"
                    className="font-mono"
                    aria-label="Memory"
                  />
                  <Input
                    value={req.cpus}
                    onChange={(e) => patch({ cpus: e.target.value })}
                    placeholder="2"
                    className="font-mono"
                    aria-label="CPUs"
                  />
                </div>
                <Hint>
                  Memory and CPUs. Empty means uncapped — and a fleet refuses to
                  start if its concurrent agents cannot fit in the host&apos;s
                  memory.
                </Hint>
              </Field>
            )}
          </div>

          {req.agent && (
            <Field label="Prompt" htmlFor="prompt">
              <Textarea
                id="prompt"
                value={req.prompt}
                onChange={(e) => patch({ prompt: e.target.value })}
                placeholder="Wire the metrics tab to the sample stream, and keep the peak summary honest when a container was never sampled."
                rows={3}
              />
              <Hint>
                {req.console
                  ? "This seeds the first turn rather than being the whole run — the session stays open, so a follow-up question can be answered by whoever attaches."
                  : "For a detached run this is the whole instruction — nobody is there to answer a follow-up question."}
              </Hint>
            </Field>
          )}
        </Section>

        {/* ---------------------------------------------------------------- */}
        <Section icon={GitBranch} title="Where it works">
          <Field label="Repository" htmlFor="repo">
            <Select
              value={req.workspace}
              onValueChange={(v) => patch({ workspace: v, worktree: null })}
            >
              <SelectTrigger id="repo">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {repoOptions.map((r) => (
                  <SelectItem key={r.id} value={r.root}>
                    {r.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>

          <Field label="Branch">
            <RadioGroup
              value={worktreeMode}
              onValueChange={(v) => setWorktreeMode(v as typeof worktreeMode)}
              className="gap-2"
            >
              <Radio
                value="main"
                label="The main checkout"
                hint="Mounts the repository itself. One agent at a time — git refuses to check out one branch in two worktrees."
              />
              <Radio
                value="new"
                label="A new worktree"
                hint="Its own directory, its own container, so agents on different branches never collide."
              />
              <Radio
                value="existing"
                label="An existing worktree"
                hint="Addressed by branch, never by directory name — an agent that ran `git checkout -b` inside its worktree would otherwise put the two out of sync."
                disabled={repoWorktrees.length === 0}
              />
            </RadioGroup>

            {worktreeMode === "new" && (
              <Input
                value={newBranch}
                onChange={(e) => setNewBranch(e.target.value)}
                placeholder="feat/my-change"
                className="mt-2 font-mono"
                aria-label="New branch name"
              />
            )}
            {worktreeMode === "existing" && (
              <Select
                value={req.worktree ?? ""}
                onValueChange={(v) => patch({ worktree: v })}
              >
                <SelectTrigger className="mt-2">
                  <SelectValue placeholder="Pick a branch" />
                </SelectTrigger>
                <SelectContent>
                  {repoWorktrees.map((w) => (
                    <SelectItem key={w.branch} value={w.branch}>
                      <span className="font-mono">{w.branch}</span>
                      {w.runId && (
                        <span className="ml-2 text-xs text-caution">
                          an agent is on it
                        </span>
                      )}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </Field>

          <Field label="Base branch" htmlFor="base">
            <Input
              id="base"
              value={req.base ?? ""}
              onChange={(e) => patch({ base: e.target.value || null })}
              placeholder="main"
              className="font-mono"
            />
            <Hint>
              Stamped as a label at launch, because by landing time the checkout
              may be on a different branch — and &ldquo;the branch checked out
              now&rdquo; is a different question from &ldquo;the branch this
              agent was sent to work towards&rdquo;.
            </Hint>
          </Field>
        </Section>

        {/* ---------------------------------------------------------------- */}
        <Section icon={ShieldCheck} title="Boundary">
          <Field label="Profile">
            <div className="grid gap-2 sm:grid-cols-2">
              {(Object.keys(PROFILES) as Profile[]).map((p) => (
                <button
                  key={p}
                  type="button"
                  onClick={() => {
                    // prod's substantive answer to the credential problem is not
                    // mounting the persisted HOME, and its baseline is off. Move
                    // the dependent switches with it rather than leaving the user
                    // to discover three refusals one at a time.
                    if (p === "prod") {
                      patch({
                        profile: p,
                        persistAuth: false,
                        sync: false,
                        network: { ...req.network, baseline: false },
                      });
                    } else {
                      patch({ profile: p });
                    }
                  }}
                  className={cn(
                    "rounded-lg border p-3 text-left transition-colors",
                    req.profile === p
                      ? "border-primary bg-primary/5"
                      : "hover:border-foreground/20 hover:bg-accent/40",
                  )}
                >
                  <div className="flex items-center gap-2">
                    <span className="font-mono text-sm font-medium">{p}</span>
                    <Badge variant="outline" className="text-[10px]">
                      {PROFILES[p].unsatisfied === "refuses"
                        ? "refuses"
                        : "warns"}
                    </Badge>
                  </div>
                  <p className="mt-1 text-xs text-muted-foreground">
                    {PROFILES[p].blurb}
                  </p>
                </button>
              ))}
            </div>
            <Hint>
              Both are secure — neither relaxes the host boundary. They differ
              in what they optimise, and in one thing of kind: dev warns when a
              control cannot be satisfied, prod refuses, because nobody is
              watching a production run.
            </Hint>
          </Field>

          <Separator />

          <Field label="Egress">
            <Select
              value={req.network.mode}
              onValueChange={(v) =>
                patch({ network: { ...req.network, mode: v as NetworkMode } })
              }
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="allowlist">
                  Allowlist — default-deny, named domains
                </SelectItem>
                <SelectItem value="none">None — reach nothing</SelectItem>
                <SelectItem value="default">Unrestricted — anything</SelectItem>
              </SelectContent>
            </Select>

            {req.network.mode === "default" && (
              <Hint tone="exposed">
                Open egress. Any credential this agent holds can leave, and the
                audit line will record that nothing was enforced.
              </Hint>
            )}

            {req.network.mode === "allowlist" && (
              <div className="space-y-3 pt-1">
                <Toggle
                  id="baseline"
                  checked={req.network.baseline}
                  onCheckedChange={(v) =>
                    patch({ network: { ...req.network, baseline: v } })
                  }
                  label={`Built-in baseline (${BASELINE_EGRESS.length} domains)`}
                  hint="The agent APIs, the common registries and the code hosts, so npm, pip and git work out of the box. It includes github.com — a write endpoint, and so an exfiltration channel for any token the agent holds."
                  tone={req.network.baseline ? "caution" : "contained"}
                />

                <div className="space-y-1.5">
                  <Label htmlFor="allow" className="text-xs">
                    Extra domains
                  </Label>
                  <TagInput
                    id="allow"
                    value={req.network.allow}
                    onChange={(allow) =>
                      patch({ network: { ...req.network, allow } })
                    }
                    placeholder="internal.example.com, then Enter"
                  />
                  <Hint>
                    Resolved fresh per connection by the in-container proxy,
                    which decides on the hostname read from the TLS SNI, a
                    CONNECT, or a Host header — so a host sharing an allowlisted
                    address does not ride in on it.
                  </Hint>
                </div>
              </div>
            )}
          </Field>

          <Separator />

          <Field label="Extra host directories">
            <TagInput
              value={req.share}
              onChange={(share) => patch({ share })}
              placeholder="/Users/you/shared, then Enter"
            />
            <Hint tone={req.share.length > 0 ? "caution" : undefined}>
              <code className="font-mono">--share</code> widens the boundary on
              purpose. It stays something you type rather than something a file
              in the repository can turn on.
            </Hint>
          </Field>

          <Field label="Forwarded host variables">
            <TagInput
              value={req.envAllow}
              onChange={(envAllow) => patch({ envAllow })}
              placeholder="ANTHROPIC_API_KEY, then Enter"
              invalid={(name) =>
                RESERVED_ENV.has(name)
                  ? "This variable is an instruction, not a setting — it cannot be forwarded from outside."
                  : null
              }
            />
            <Hint>
              Forwarded only if set on the host, and recorded by name only.
              {agentMeta && agentMeta.envAllow.length > 0 && (
                <>
                  {" "}
                  {agentMeta.label} suggests{" "}
                  <span className="font-mono">
                    {agentMeta.envAllow.join(", ")}
                  </span>
                  .
                </>
              )}
            </Hint>
          </Field>
        </Section>

        {/* ---------------------------------------------------------------- */}
        <Section icon={Terminal} title="Autonomy">
          <Toggle
            id="detach"
            checked={req.detach}
            onCheckedChange={(detach) => patch({ detach })}
            label="Run detached"
            hint="Nobody is attached, so `-d` replaces `-i`/`-it` and the container is not removed on exit — the exit code and its logs are the entire supervision story."
          />

          {req.console && req.agent && <ResumePicker req={req} patch={patch} />}

          {/*
            Three states, and each says something different about the run.

            Checked and locked when the agent has a skip flag and there is no
            console: that is what the launch actually does, since Autonomous
            appends the flag and an agent that stops for permission with nobody
            attached hangs rather than fails. Rendering it unchecked there
            described the opposite of the run being started.

            Unchecked and locked when the agent has no such flag at all. Its
            non-interactive mode is a subcommand, sandbox-cli adds nothing, and
            whether it asks is the agent's own policy — codex says so in its
            descriptor. Claiming "always on" for those would be the same false
            statement pointed the other way.

            A real choice only for a console run of an agent that has the flag,
            which is the one case where somebody is attached and could answer.
          */}
          <Toggle
            id="skip-permissions"
            checked={headlessAlwaysSkips || req.skipPermissions}
            disabled={headlessAlwaysSkips || !req.console || !agentMeta?.canSkipPermissions}
            onCheckedChange={(skipPermissions) => patch({ skipPermissions })}
            label="Let it work without asking"
            tone={headlessAlwaysSkips || req.skipPermissions ? "caution" : undefined}
            hint={
              headlessAlwaysSkips
                ? `Always on for a headless run, and not a choice: ${agentMeta?.label ?? req.agent} is started in its autonomous argv${skipFlag ? ` (${skipFlag})` : ""}, because an agent that stops for permission with nobody attached does not fail — it hangs. Keep a console below if you want to be asked.`
                : !req.agent
                  ? "Pick an agent first. A plain command is whatever argv you typed; there are no approval prompts to turn off."
                  : agentMeta?.canSkipPermissions === false
                    ? `${agentMeta?.label ?? req.agent}'s non-interactive mode is a subcommand rather than a flag, so sandbox-cli adds nothing here — whether it stops to ask is its own approval policy, headless or not.`
                    : `Adds ${skipFlag ?? "the agent's skip-permissions flag"}, so an attached session runs to the end instead of waiting for you. The container is the blast-radius boundary either way — this changes what it asks, not what it can reach.`
            }
          />

          <Toggle
            id="console"
            checked={req.console}
            disabled={!req.agent}
            onCheckedChange={(console) => patch({ console })}
            label="Keep a console I can attach to"
            hint={
              req.agent
                ? "Starts the agent's interactive mode on a container that keeps a terminal (`-dit`), so `sandbox-cli attach` from any window can answer it. Without this the agent runs headless: it produces one final answer and can never stop to ask."
                : "Needs an agent. A plain command is already whatever argv you typed — there is no headless mode to swap out of."
            }
          />

          <Field label="Verify command" htmlFor="verify">
            <Input
              id="verify"
              value={req.verify}
              onChange={(e) => patch({ verify: e.target.value })}
              placeholder="make test"
              className="font-mono"
              disabled={req.console}
            />
            <Hint>
              {req.console ? (
                <>
                  Not available with a console. Verify decides the run&apos;s
                  exit code, which is how <code>land</code> knows the work is
                  done — and an interactive session&apos;s exit code is whenever
                  you quit. Run it yourself in the session instead.
                </>
              ) : (
                <>
                  Wrapped around the agent&apos;s argv <em>inside</em> the
                  container, and its exit code becomes the container&apos;s. In
                  the container because a verify running on the host would be
                  host code selected by a file the agent can write.
                </>
              )}
            </Hint>
          </Field>

          {req.agent && (
            <>
              <Separator />
              <Toggle
                id="persist"
                checked={req.persistAuth}
                onCheckedChange={(persistAuth) => patch({ persistAuth })}
                label="Persist the agent login"
                hint="Bind-mounts a sandbox-owned host directory as the agent's whole HOME, separate from your real ~/.claude. This is where the OAuth refresh token lives, readable by the agent — which is exactly what prod declines to mount."
                tone={req.persistAuth ? "caution" : "contained"}
                disabled={req.profile === "prod"}
              />
              {req.agent === "claude" && (
                <>
                  <Toggle
                    id="sync"
                    checked={req.sync}
                    onCheckedChange={(sync) => patch({ sync })}
                    label="Sync this project's Claude history"
                    hint="Mounts the host's ~/.claude/projects bucket for this project so sessions resolve on both sides. The one default that reaches a host path outside the workspace — and it is created if absent, or every sandbox session would pool into a shared bucket findable by no project."
                    tone={req.sync ? "caution" : undefined}
                    disabled={req.profile === "prod"}
                  />
                  <Toggle
                    id="statusline"
                    checked={req.statusline}
                    onCheckedChange={(statusline) => patch({ statusline })}
                    label="Status line"
                    hint="Injected through a read-only managed-settings.json, which never touches your own Claude settings. Carries memory, CPU, the model and the subscription windows."
                  />
                </>
              )}
            </>
          )}
        </Section>

        <div className="flex flex-wrap items-center gap-3">
          <Button
            size="lg"
            onClick={submit}
            disabled={blocked || launch.isPending}
          >
            <Play className="size-4" />
            {launch.isPending ? "Starting…" : "Launch sandbox"}
          </Button>
          {blocked && (
            <p className="text-xs text-destructive">
              {preview.refusals.length === 1
                ? "One refusal to resolve first."
                : `${preview.refusals.length} refusals to resolve first.`}
            </p>
          )}
        </div>
      </div>

      <aside className="space-y-4 xl:sticky xl:top-20 xl:self-start">
        <LaunchPreview preview={preview} />
      </aside>
    </div>
  );
}

function Section({
  icon: Icon,
  title,
  children,
}: {
  icon: typeof Bot;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <Card className="surface-sheen gap-4">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-sm font-medium">
          <Icon className="size-4 text-muted-foreground" />
          {title}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">{children}</CardContent>
    </Card>
  );
}

function Field({
  label,
  htmlFor,
  children,
}: {
  label: string;
  htmlFor?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={htmlFor} className="text-xs">
        {label}
      </Label>
      {children}
    </div>
  );
}

function Hint({
  children,
  tone,
}: {
  children: React.ReactNode;
  tone?: "caution" | "exposed" | "contained";
}) {
  return (
    <p
      className={cn(
        "text-xs leading-relaxed",
        tone === "caution"
          ? "text-caution"
          : tone === "exposed"
            ? "text-exposed"
            : tone === "contained"
              ? "text-contained"
              : "text-muted-foreground",
      )}
    >
      {children}
    </p>
  );
}

function Toggle({
  id,
  checked,
  onCheckedChange,
  label,
  hint,
  tone,
  disabled,
}: {
  id: string;
  checked: boolean;
  onCheckedChange: (v: boolean) => void;
  label: string;
  hint: string;
  tone?: "caution" | "contained";
  disabled?: boolean;
}) {
  return (
    <div className="flex items-start gap-3">
      <Switch
        id={id}
        checked={checked}
        onCheckedChange={onCheckedChange}
        disabled={disabled}
        className="mt-0.5"
      />
      <div className="min-w-0 space-y-0.5">
        <Label
          htmlFor={id}
          className={cn(
            "text-xs font-medium",
            tone === "caution" && checked && "text-caution",
            tone === "contained" && !checked && "text-contained",
            disabled && "opacity-60",
          )}
        >
          {label}
          {disabled && (
            <span className="ml-1.5 font-normal opacity-70">
              (prod decides this)
            </span>
          )}
        </Label>
        <p className="text-xs leading-relaxed text-muted-foreground">{hint}</p>
      </div>
    </div>
  );
}

function Radio({
  value,
  label,
  hint,
  disabled,
}: {
  value: string;
  label: string;
  hint: string;
  disabled?: boolean;
}) {
  return (
    <div className="flex items-start gap-3">
      <RadioGroupItem
        value={value}
        id={`wt-${value}`}
        disabled={disabled}
        className="mt-0.5"
      />
      <div className="min-w-0 space-y-0.5">
        <Label
          htmlFor={`wt-${value}`}
          className={cn("text-xs font-medium", disabled && "opacity-60")}
        >
          {label}
        </Label>
        <p className="text-xs leading-relaxed text-muted-foreground">{hint}</p>
      </div>
    </div>
  );
}

/**
 * Pick a conversation to carry on instead of starting one.
 *
 * Console-only, because that is the daemon's rule and the reason for it holds
 * here too: a headless resume would replay one prompt into an old conversation
 * and exit, which is not what anyone means by carrying it on.
 *
 * Only the sandbox-owned store is offered. Those are the conversations a
 * container can actually reopen — your own ~/.claude history is a real store
 * and resuming it here would mean mounting the host's history into a container
 * that was not asked to have it.
 */
function ResumePicker({
  req,
  patch,
}: {
  req: LaunchRequest;
  patch: (p: Partial<LaunchRequest>) => void;
}) {
  const { data: sessions, isPending } = useAgentSessions(req.agent);

  if (isPending) return <Skeleton className="h-9 w-full rounded-md" />;
  if (!sessions || sessions.length === 0) {
    return (
      <Hint>
        No conversations to resume yet — {req.agent} has not written one in the
        sandbox-owned agent HOME. The first console run here creates one.
      </Hint>
    );
  }

  return (
    <Field label="Resume a conversation" htmlFor="resume">
      <Select
        value={req.resume ?? "none"}
        onValueChange={(v) => patch({ resume: v === "none" ? null : v })}
      >
        <SelectTrigger id="resume">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="none">Start a new conversation</SelectItem>
          {sessions.map((sess: SessionSummary) => (
            <SelectItem key={sess.id} value={sess.id}>
              {/* Text written by an agent, rendered and never interpreted. A
                  partial session has no readable title, and says so rather
                  than showing an empty row. */}
              {sess.title || (sess.partial ? "(unread format)" : "(untitled)")}
              {" · "}
              {sess.partial ? "?" : pluralize(sess.turns, "turn")}
              {" · "}
              {formatRelative(sess.modified)}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Hint>
        {req.resume
          ? "The prompt above is ignored: the conversation already has one, and this reopens it where it stopped."
          : "Reopens an earlier session in a fresh container. Only conversations the sandbox itself wrote are listed."}
      </Hint>
    </Field>
  );
}
