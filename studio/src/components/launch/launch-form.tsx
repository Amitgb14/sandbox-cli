"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import {
  Bot,
  FolderPlus,
  GitBranch,
  Play,
  ShieldCheck,
  Terminal,
} from "lucide-react";
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
  useProjects,
  useRemoveRun,
  useWorktrees,
  useBranches,
} from "@/lib/api/queries";
import { AddRepositoryDialog } from "@/components/shell/add-repository-dialog";
import { localPreview } from "@/lib/api/endpoints";
import { formatRelative, pluralize } from "@/lib/format";
import { PROFILES, RESERVED_ENV } from "@/lib/constants";
import { useUi } from "@/lib/store";
import type {
  AgentName,
  LaunchRequest,
  Profile,
  SessionSummary,
} from "@/lib/types";
import { cn } from "@/lib/utils";

/**
 * What the form holds — every field of a launch request except `console`.
 *
 * `console` was a toggle and is now **derived**, so it is omitted here rather
 * than defaulted: a field nothing may set is one the next reader wires a
 * control back onto. The rule that replaced the toggle is `consoleRun` below.
 */
type FormState = Omit<LaunchRequest, "console">;

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
/** The posture in the words the CLI uses for it. */
function egressLabel(mode: string): string {
  if (mode === "allowlist") return "allowlist — default-deny";
  if (mode === "none") return "none — reaches nothing";
  return "unrestricted";
}

/**
 * Radix Select cannot carry "" as a value, and the base's own empty value means
 * "let the daemon decide" — a real choice rather than an absence. So the option
 * gets a sentinel and is translated back at the edge.
 */
const DEFAULT_BASE = "__default__";

export function LaunchForm() {
  // The daemon's own egress posture, which a launch reports rather than sets.
  const { data: daemon } = useDaemon();
  const egress = daemon?.egress;
  const router = useRouter();
  const search = useSearchParams();
  const repoFilter = useUi((s) => s.repoFilter);
  const routingPrefs = useUi((s) => s.routingPrefs);
  const setRoutingPref = useUi((s) => s.setRoutingPref);
  const { data: agents } = useAgents();
  const { data: projects } = useProjects();
  const [addRepoOpen, setAddRepoOpen] = useState(false);
  const launch = useLaunchRun();
  const removeRun = useRemoveRun();

  const initialAgent = (search.get("agent") as AgentName | null) ?? "claude";
  /**
   * `?resume=` arrives from a conversation row's **Continue**, with `?agent=`
   * and — when the conversation could be attributed — `?repo=`.
   *
   * It no longer has to set the console: an agent run keeps one by default, and
   * the daemon refuses a headless resume outright — replaying one prompt into an
   * old conversation and exiting is not what anyone means by carrying it on. The
   * link therefore lands on a form that can be submitted as it arrived, and
   * `localPreview` still refuses if something headless-only is typed underneath.
   *
   * Applied at mount rather than in an effect: unlike `?branch=`, neither value
   * has to be matched against a list the daemon has not sent yet.
   */
  const initialResume = search.get("resume");
  const initialRepo = search.get("repo");
  /**
   * `?handoffAgent=` + `?handoffSession=` arrive from a conversation row when
   * the agent picked is **not** the one that held it — or is, but cannot reopen
   * a session by id (gemini has no resume argv, so a briefing from
   * itself is the only way to carry one of theirs on).
   *
   * Unlike a resume this does not set the console: a handoff starts a new
   * conversation, so headless is a legitimate way to run it and forcing an
   * interactive session would be choosing for somebody.
   */
  const initialHandoffAgent = search.get("handoffAgent");
  const initialHandoffSession = search.get("handoffSession");
  const routingPrefsAtMount = useRef(routingPrefs).current;

  const [req, setReq] = useState<FormState>({
    agent: initialAgent,
    command: "",
    prompt: "",
    // Both empty until the daemon has answered. There is nothing honest to put
    // here before then: a repository this form invented is one the daemon has
    // never heard of, which is exactly how a path from a fixture ended up in a
    // real launch request.
    // The link's repository outranks the sidebar's scope: it says which tree
    // this conversation happened in, and resuming it against another one is the
    // failure that would be hardest to see afterwards. Empty when the session
    // could not be attributed, which leaves the picker asking.
    repo: initialRepo ?? repoFilter ?? "",
    // Seeded from the remembered choice for this agent, so a fallback set once
    // is still there next time rather than something to re-pick on every launch.
    fallback: initialAgent ? (routingPrefsAtMount[initialAgent] ?? []) : [],
    workspace: "",
    worktree: null,
    // Not "main": that is a guess at a name, and the base is stamped as the
    // label `fleet land` reads back to decide what to merge into. A repository
    // whose default is `master` would have been launched with a base that does
    // not exist there, silently. null means the daemon's own default until the
    // branch list arrives.
    base: null,
    profile: "dev",
    network: { mode: "allowlist", baseline: true, allow: [] },
    memory: "4g",
    cpus: "2",
    /**
     * True, because the console being the default would otherwise have made the
     * default launch do nothing.
     *
     * This used to be false and locked *on* in the UI: a headless run gets the
     * flag from `Descriptor.Autonomous` whatever the form says, so clicking
     * Launch and walking away produced work. A console run takes
     * `agent.Console(prompt, skipPermissions)` instead, so the same click now
     * starts claude's interactive UI — which stops at its first tool approval,
     * in a detached container, with nobody attached yet.
     *
     * So this is not a widening: it is the autonomy the default launch already
     * had, moved from locked-on to *checked and changeable*, which is strictly
     * more control than before. Untick it and the session waits to be answered,
     * which is now a thing somebody can ask for.
     */
    skipPermissions: true,
    resume: initialResume,
    handoffFrom:
      initialHandoffAgent && initialHandoffSession
        ? { agent: initialHandoffAgent, sessionId: initialHandoffSession }
        : null,
    persistAuth: true,
    sync: true,
    statusline: true,
    verify: "",
    envAllow: [],
    share: false,
    shareName: "",
    publish: [],
  });

  // Adopt a repository as soon as the daemon lists one: whichever the app is
  // scoped to, else the one the daemon was started in, else the first that can
  // actually be read. Only while nothing is chosen — this must never move a
  // selection out from under someone mid-form.
  useEffect(() => {
    if (!projects?.length) return;
    setReq((prev) => {
      if (prev.repo && projects.some((p) => p.id === prev.repo)) return prev;
      const usable = projects.filter((p) => !p.missing);
      const pick =
        usable.find((p) => p.id === repoFilter) ??
        usable.find((p) => p.default) ??
        usable[0];
      if (!pick) return prev;
      return { ...prev, repo: pick.id, workspace: pick.root };
    });
  }, [projects, repoFilter]);
  // Scoped to the repository *this form* has picked, which is not necessarily
  // the one the sidebar has scoped the app to — choosing where the agent works
  // is the question this screen exists to answer.
  const { data: worktrees } = useWorktrees(req.repo || undefined);

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
  // The repository's branches, for the base picker. Scoped to whatever the form
  // is pointed at, so switching repository re-asks rather than offering the
  // previous one's names.
  const { data: branches } = useBranches(req.repo ?? undefined);

  /**
   * A base the repository does not have is not a base.
   *
   * Radix renders the *selected item*, so a value with no matching option shows
   * as blank while still being sent — which is how a stale name survives a
   * repository switch and gets stamped as the label. Cleared to the daemon's
   * default instead, which is a name the run will actually be landed into.
   */
  useEffect(() => {
    if (!branches || !req.base) return;
    if (!branches.branches.includes(req.base)) patch({ base: null });
  }, [branches, req.base]);

  const deepLinkBranch = search.get("branch");
  const deepLinkApplied = useRef(false);

  useEffect(() => {
    if (deepLinkApplied.current || !deepLinkBranch || !worktrees) return;
    const match = worktrees.find(
      (w) => !w.primary && w.branch === deepLinkBranch,
    );
    if (!match) {
      /**
       * A branch with no worktree yet, which is what a restore hands back: it
       * creates a *branch* and stops, so following its Continue link found
       * nothing here and silently applied none of the deep link — the launch
       * then ran on main, against the files the restore existed to replace.
       *
       * Asking for it as a new worktree is the same request the CLI's
       * `--worktree <branch>` makes, and the daemon resolves it the same way:
       * an existing branch is checked out into a worktree of its own rather
       * than created afresh.
       */
      deepLinkApplied.current = true;
      setNewBranch(deepLinkBranch);
      setWorktreeMode("new");
      return;
    }
    deepLinkApplied.current = true;
    // Deliberately touches neither `repo` nor `workspace`. It used to set the
    // workspace from REPOS, which was fixture data — and the fixture's id
    // matched the real repo id, so the lookup *succeeded* and quietly replaced
    // the daemon's project with a path that exists on nobody's disk. The launch
    // then failed with "project path does not exist".
    //
    // A deep link carries a branch, and a branch is all it should apply. The
    // repository is already whichever one the app was scoped to when the link
    // was followed — and the worktree list this branch was matched against is
    // that repository's, so the two cannot disagree.
    setReq((prev) => ({
      ...prev,
      worktree: match.branch,
      base: match.base ?? prev.base,
    }));
    setWorktreeMode("existing");
  }, [deepLinkBranch, worktrees]);

  function patch(next: Partial<FormState>) {
    setReq((prev) => ({ ...prev, ...next }));
  }

  const agentMeta = agents?.find((a) => a.name === req.agent);

  /**
   * Whether this launch keeps a console — **derived**, not chosen.
   *
   * An agent run is interactive by default now: the container keeps a terminal
   * (`-dit`) and the prompt seeds a first turn rather than being the whole run,
   * so whoever attaches can answer a follow-up question. The toggle that used to
   * ask is gone, and the four exceptions below are not a hidden preference —
   * every one of them is a pair the daemon **refuses**, so a console here would
   * be a 400 rather than a different run.
   *
   * - No agent. A plain command is whatever argv was typed; `runs.go` refuses a
   *   console without an agent, because there is no interactive mode to swap in.
   * - A verify command. Verify's exit code is the answer it exists to give, and
   *   an interactive session's exit code is whenever somebody quit — so typing
   *   one is now how you ask for a headless run.
   * - A fallback chain. Routing retries a run that exited non-zero having
   *   changed nothing, which is a claim about a run that ends by itself.
   * - A prompt for an agent that cannot seed one (`canSeedConsolePrompt`:
   *   opencode reads a lone positional as the directory to open, so its
   *   interactive argv has nowhere to put a first turn). Headless is the mode
   *   that can carry the prompt, and the prompt is the instruction.
   *
   * `undefined` counts as "can", and Launch is disabled until the list arrives
   * (`agentsPending`) rather than that being read as an all-clear: `?agent=` is
   * a deep link, so the name does **not** have to have come from the picker —
   * `?agent=cline` plus the prompt a handoff requires is exactly the pair
   * `runs.go` refuses, and guessing either way in that window would make it
   * reachable. Waiting is the only answer that cannot be wrong.
   */
  const consoleRun =
    !!req.agent &&
    !req.verify.trim() &&
    req.fallback.length === 0 &&
    (!req.prompt.trim() || agentMeta?.canSeedConsolePrompt !== false);

  // An agent named in the URL whose descriptor has not arrived. Nothing about
  // the request is known yet — neither the mode nor whether the daemon would
  // take it — so the button waits rather than the derivation guessing.
  const agentsPending = !!req.agent && !agentMeta;

  const resolved: LaunchRequest = useMemo(
    () => ({
      ...req,
      console: consoleRun,
      worktree:
        worktreeMode === "main"
          ? null
          : worktreeMode === "new"
            ? newBranch || null
            : req.worktree,
    }),
    [req, consoleRun, worktreeMode, newBranch],
  );

  const preview = useMemo(
    () => localPreview(resolved, egress),
    [resolved, egress],
  );
  const blocked = preview.refusals.length > 0;

  // An agent run with no console skips permissions whatever this form says —
  // *for the agents that have a flag to skip with*. `Descriptor.Autonomous`
  // appends `SkipPermissionArgs`, and that is empty for codex and opencode,
  // whose non-interactive mode is a subcommand. Codex in particular
  // "applies its own approval policy on top" (its descriptor says so) and
  // sandbox-cli deliberately does not relax it, so claiming the run works
  // without asking would be the same false statement this control was fixed to
  // stop making, pointed the other way. A plain command is excluded too: no
  // agent, nothing to ask.
  const headlessAlwaysSkips =
    !!req.agent && !consoleRun && agentMeta?.canSkipPermissions === true;
  const skipFlag = agentMeta?.skipPermissionArgs?.join(" ");
  // The repositories the daemon answers about, and nothing else. Addressed by
  // repo **id**, never by a name derived from the workspace path: two clones of
  // a same-named repo would otherwise share a namespace — and the name-derived
  // version was already wrong here, because a worktree path carries the id
  // (`intrupt-web-1f3ab902`) while the workspace directory carries the name
  // (`intrupt_web`), so it matched nothing for those repos.
  //
  // A repository that cannot be read is listed and not selectable: hiding it
  // would leave someone hunting for the one they added, and offering it would
  // launch against a directory that is not there.
  const repoOptions = projects ?? [];
  // Already scoped by the query above, so every worktree here belongs to the
  // picked repository; only the primary checkout is dropped.
  const repoWorktrees = (worktrees ?? []).filter((w) => !w.primary);

  function submit() {
    launch.mutate(resolved, {
      onSuccess: ({ id }) => {
        toast.success("Sandbox starting", {
          // Every run launched here is detached — the daemon sets it, and this
          // used to read `req.detach`, a field that was always false, so the
          // toast said "Attached" about every run Studio has ever started. What
          // differs is whether there is a console to attach *to*.
          description: consoleRun
            ? "Detached with a console — open the Terminal tab to answer it."
            : "Detached and headless — follow it from Runs, and its exit code is the whole supervision story.",
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
                onValueChange={(v) => {
                  const next = v === "__none" ? null : (v as AgentName);
                  // The fallback belongs to the primary, so switching the primary
                  // brings that agent's remembered answer rather than carrying
                  // the previous agent's — which would silently pair two agents
                  // nobody put together.
                  //
                  // The conversation is dropped for the sharper version of the
                  // same reason: a session id is a primary key into *one*
                  // vendor's private store, so carrying it across agents asks
                  // codex to reopen a conversation claude wrote. That was always
                  // reachable by picking a session and then changing the agent;
                  // arriving from a conversation row's Continue makes it the
                  // common path, since the form now lands with one already set.
                  patch({
                    agent: next,
                    fallback: next ? (routingPrefs[next] ?? []) : [],
                    resume: null,
                  });
                }}
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
              {/*
                Gated on the run being headless, which is the fact this is about.
                It used to be gated on `req.detach` — always false, so this never
                rendered at all, for the agents it exists to warn about.
              */}
              {agentMeta && !agentMeta.headlessVerified && !consoleRun && (
                <Hint tone="caution">
                  {agentMeta.label} has no verified headless argv, and this run
                  is headless. An agent that stops to ask permission with nobody
                  attached does not fail — it hangs.
                </Hint>
              )}
              {agentMeta && agentMeta.delivery !== "baked" && (
                <Hint>
                  Installed on first run into the persisted HOME (
                  {agentMeta.delivery}), so the image carries nothing for it.
                </Hint>
              )}
            </Field>

            {req.agent && (
              <Field label="If its provider is down" htmlFor="fallback">
                <Select
                  value={req.fallback[0] ?? "__none"}
                  onValueChange={(v) => {
                    const chain = v === "__none" ? [] : [v];
                    patch({ fallback: chain });
                    // Remembered immediately rather than on submit: a choice made
                    // and then abandoned is still the answer you would give next
                    // time, and a preference that only saves on success is one
                    // that quietly forgets whenever a launch is refused.
                    if (req.agent) setRoutingPref(req.agent, chain);
                  }}
                >
                  <SelectTrigger id="fallback">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      <SelectLabel>No fallback</SelectLabel>
                      <SelectItem value="__none">Fail instead</SelectItem>
                    </SelectGroup>
                    <SelectGroup>
                      <SelectLabel>Fall back to</SelectLabel>
                      {agents
                        // Only verified-headless agents, and only other ones. A
                        // Studio run is detached, so an agent that stops to ask
                        // permission hangs with nobody to answer — the same rule
                        // a fleet applies.
                        ?.filter(
                          (a) => a.headlessVerified && a.name !== req.agent,
                        )
                        .map((a) => (
                          <SelectItem key={a.name} value={a.name}>
                            {a.label}
                          </SelectItem>
                        ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
                {req.fallback.length > 0 && (
                  <Hint>
                    Studio checks the provider before launching and starts{" "}
                    {agents?.find((a) => a.name === req.fallback[0])?.label ??
                      req.fallback[0]}{" "}
                    instead if it is not answering — and if this run fails later
                    having written nothing, hands the work over with a briefing
                    of what was said. A run that changed files is never retried.
                    The fallback runs with its own login and its own transcript.
                  </Hint>
                )}
              </Field>
            )}

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

          {req.handoffFrom && (
            <BriefingNotice
              from={req.handoffFrom}
              to={req.agent}
              onClear={() => patch({ handoffFrom: null })}
            />
          )}

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
                {req.handoffFrom
                  ? "Required for a handoff, and it is the half the briefing does not carry: the briefing says what happened before, this says what to do now."
                  : consoleRun
                    ? "This seeds the first turn rather than being the whole run — the session stays open, so a follow-up question can be answered by whoever attaches."
                    : "For a detached run this is the whole instruction — nobody is there to answer a follow-up question."}
              </Hint>
            </Field>
          )}
        </Section>

        {/* ---------------------------------------------------------------- */}
        <Section icon={GitBranch} title="Where it works">
          <Field label="Repository" htmlFor="repo">
            <div className="flex gap-2">
              <Select
                value={req.repo}
                onValueChange={(v) => {
                  const picked = repoOptions.find((r) => r.id === v);
                  // The workspace follows the id rather than being chosen
                  // beside it: they are one fact, and two controls for one fact
                  // is how they end up disagreeing.
                  patch({
                    repo: v,
                    workspace: picked?.root ?? "",
                    worktree: null,
                  });
                }}
              >
                <SelectTrigger id="repo" className="flex-1">
                  <SelectValue placeholder="Waiting for the daemon…" />
                </SelectTrigger>
                <SelectContent>
                  {repoOptions.map((r) => (
                    <SelectItem key={r.id} value={r.id} disabled={r.missing}>
                      {r.name}
                      {r.missing ? " — unavailable" : ""}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Button
                type="button"
                variant="outline"
                size="icon"
                title="Add a repository"
                aria-label="Add a repository"
                onClick={() => setAddRepoOpen(true)}
              >
                <FolderPlus className="size-4" />
              </Button>
            </div>
            {req.workspace && (
              // The host path that will be mounted at /workspace. Shown because
              // the name alone cannot tell two checkouts of one repository apart,
              // and this is the last screen before a container reaches it.
              <p className="mt-1.5 truncate font-mono text-[11px] text-muted-foreground">
                {req.workspace}
              </p>
            )}
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
            {/*
              A picker rather than a text box. The base is stamped as a label at
              launch and `fleet land` reads it back to decide what to merge into,
              so a typo is not caught until landing — by which point the run has
              happened against the wrong recorded intent. The list is the
              repository's branches, not its worktrees: the base is usually the
              default branch, which most often has no worktree of its own.

              Empty stays a real choice — the daemon's own default — so a
              repository whose branches cannot be listed is not a launch nobody
              can start.
            */}
            <Select
              value={req.base ?? ""}
              onValueChange={(v) =>
                patch({ base: v === DEFAULT_BASE ? null : v })
              }
            >
              <SelectTrigger id="base" className="font-mono">
                <SelectValue placeholder={branches?.current ?? "main"} />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={DEFAULT_BASE}>
                  <span className="text-muted-foreground">
                    default{branches?.current ? ` (${branches.current})` : ""}
                  </span>
                </SelectItem>
                {(branches?.branches ?? []).map((b) => (
                  <SelectItem key={b} value={b}>
                    <span className="font-mono">{b}</span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
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

          {/* Read-only, because the request cannot express it.
              `network.mode` is the daemon's own resolved posture: a launch may
              *add* domains and may never loosen the mode, the same tighten-only
              rule internal/config/trust.go applies to a project file. This was a
              Select — initialised to a hardcoded "allowlist", changing nothing,
              and reflecting nothing. Selecting "Unrestricted" and still being
              blocked is what a control that lies feels like from the outside. */}
          <Field label="Egress">
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant="outline" className="font-mono text-[11px]">
                {egress ? egressLabel(egress.mode) : "…"}
              </Badge>
              {egress?.mode === "allowlist" && (
                <span className="text-xs text-muted-foreground">
                  {egress.domains ?? egress.allow?.length ?? 0} domains
                  {egress.baseline ? ", baseline included" : ", baseline off"}
                </span>
              )}
            </div>

            {egress?.mode === "default" && (
              <Hint tone="exposed">
                Open egress. Any credential this agent holds can leave, and the
                audit line will record that nothing was enforced.
              </Hint>
            )}
            {egress?.mode === "none" && (
              <Hint tone="contained">
                This daemon launches with no network at all.
              </Hint>
            )}

            <Hint>
              Set where the daemon reads its configuration, not per run — it is
              tighten-only from here, so a request can add domains and cannot
              open the posture. Change it in{" "}
              <code className="font-mono">~/.config/sandbox/config.yaml</code>{" "}
              (or the <code className="font-mono">--config</code> file the
              daemon was started with) and restart it.
            </Hint>

            {/* Not offered on a daemon configured to reach nothing. `allow` is
                read by BuildSpec as switching the allowlist *on*, which promotes
                the container off `--network none` — so this field there would
                widen the posture rather than narrow it, and the daemon refuses
                it for exactly that reason. */}
            {egress?.mode !== "none" && (
              <div className="space-y-1.5 pt-1">
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
                <Hint tone={egress?.mode === "default" ? "caution" : undefined}>
                  {egress?.mode === "default"
                    ? "Adding a domain here switches the allowlist on for this run — on an unrestricted daemon that tightens the run rather than widening it, which is the only direction a request may move."
                    : "Resolved fresh per connection by the in-container proxy, which decides on the hostname read from the TLS SNI, a CONNECT, or a Host header — so a host sharing an allowlisted address does not ride in on it."}
                </Hint>
              </div>
            )}
          </Field>

          <Separator />

          <Toggle
            id="share"
            checked={req.share}
            onCheckedChange={(share) =>
              patch({ share, shareName: share ? req.shareName : "" })
            }
            label="Share the handoff directory"
            tone={req.share ? "caution" : undefined}
            hint="Mounts ~/.config/sandbox/shared at /shared. It is the only way two sandboxes can exchange a file — everything else a run sees is scoped to its own project — so it widens the boundary on purpose. It stays something you turn on here rather than something a file in the repository can."
          />

          {req.share && (
            <Field label="Namespace (optional)">
              <Input
                value={req.shareName}
                onChange={(e) => patch({ shareName: e.target.value })}
                placeholder="work"
              />
              <Hint>
                Mounts <code className="font-mono">/shared/NAME</code> instead
                of the root, so two runs that both want a handoff spot do not
                clobber the same filename. It prevents collisions, not access:
                any run sharing the root can read every namespace in it.
              </Hint>
            </Field>
          )}

          <Field label="Published ports">
            <TagInput
              value={req.publish}
              onChange={(publish) => patch({ publish })}
              placeholder="8000, or 8080:8000, then Enter"
            />
            <Hint tone={req.publish.length > 0 ? "caution" : undefined}>
              For reaching a dev server the agent starts. A bare port binds{" "}
              <code className="font-mono">127.0.0.1</code> on the machine
              running the daemon — not every interface, which is where this
              differs from <code className="font-mono">docker -p</code>; write
              an address out to say otherwise. Under an allowlist the firewall
              opens its default-deny inbound chain for exactly these ports,
              which is the one way a launch option lets anything <em>in</em>.
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
          {/*
            "Run detached" used to be a toggle here and has been removed rather
            than relabelled. It never travelled — `buildLaunchBody` sends no
            detach field and `runs.go` sets `Detach: true` on every request,
            because an HTTP request/response cycle has nowhere to hold a pty —
            so the only thing it changed was the preview beside it, which is the
            one place it must not. Unticked, it described a run with a pty that
            the daemon was never asked for; and it sat directly above a line
            asserting this run keeps `-dit`, which is the contradiction that
            found it.
          */}

          {/*
            Rendered while a conversation is *set* as well as while one can be
            picked. Typing a verify command under a resumed session flips the run
            headless, and the daemon refuses a headless resume — so unmounting
            the picker there left a refusal offering two remedies, one of which
            was "clear the conversation" with no control on screen to clear it
            with. The picker stays, and says why it cannot be used.
          */}
          {req.agent && (consoleRun || req.resume) && (
            <ResumePicker req={req} patch={patch} headless={!consoleRun} />
          )}

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
            /*
              Now that this defaults to *true*, the two states where it means
              nothing have to say so: an agent whose skip list is empty
              (`canSkipPermissions === false` — sandbox-cli adds nothing, the
              approval policy is the agent's own) and no agent at all. Rendering
              it checked there would be the same false statement the hint beside
              it was written to stop making, which is why the default could not
              simply be read off the state.
            */
            checked={
              headlessAlwaysSkips ||
              (!!req.agent &&
                agentMeta?.canSkipPermissions !== false &&
                req.skipPermissions)
            }
            disabled={
              headlessAlwaysSkips ||
              !consoleRun ||
              !agentMeta?.canSkipPermissions
            }
            onCheckedChange={(skipPermissions) => patch({ skipPermissions })}
            label="Let it work without asking"
            tone={
              headlessAlwaysSkips || req.skipPermissions ? "caution" : undefined
            }
            hint={
              headlessAlwaysSkips
                ? `Always on for a headless run, and not a choice: ${agentMeta?.label ?? req.agent} is started in its autonomous argv${skipFlag ? ` (${skipFlag})` : ""}, because an agent that stops for permission with nobody attached does not fail — it hangs. The line below says what made this run headless; clear that for a console, and being asked becomes a choice.`
                : !req.agent
                  ? "Pick an agent first. A plain command is whatever argv you typed; there are no approval prompts to turn off."
                  : agentMeta?.canSkipPermissions === false
                    ? `${agentMeta?.label ?? req.agent}'s non-interactive mode is a subcommand rather than a flag, so sandbox-cli adds nothing here — whether it stops to ask is its own approval policy, headless or not.`
                    : `Adds ${skipFlag ?? "the agent's skip-permissions flag"}, so an attached session runs to the end instead of waiting for you. The container is the blast-radius boundary either way — this changes what it asks, not what it can reach.`
            }
          />

          {/*
            What used to be a toggle. An agent run keeps a console by default,
            so this states the mode rather than asking for it — and names the
            field to clear when the mode is not the one you wanted, since every
            exception is something else on this form having been filled in.
          */}
          <div className="rounded-md border border-border/60 bg-muted/30 px-3 py-2">
            <Hint tone={consoleRun ? "contained" : undefined}>
              {!req.agent ? (
                <>
                  <strong className="font-medium">No console.</strong> A plain
                  command is whatever argv you typed — there is no interactive
                  mode to swap in, and the daemon refuses a console without an
                  agent.
                </>
              ) : consoleRun ? (
                <>
                  <strong className="font-medium">
                    Keeps a console you can attach to.
                  </strong>{" "}
                  The agent starts in its interactive mode on a container that
                  holds a terminal (<code>-dit</code>), so this window or{" "}
                  <code>sandbox-cli attach</code> can answer it. The prompt
                  seeds the first turn instead of being the whole run.{" "}
                  {/*
                    Whether it gets anywhere before somebody attaches is the
                    other half, and it is the half an interactive run changed:
                    unticked, the session stops at its first approval in a
                    container nobody is watching yet.
                  */}
                  {agentMeta?.canSkipPermissions === false
                    ? `Whether it stops to ask is ${agentMeta?.label ?? req.agent}'s own approval policy — sandbox-cli adds nothing there.`
                    : req.skipPermissions
                      ? "It works through without asking, so it makes progress before anyone attaches."
                      : "It will stop at its first approval and wait — nobody is attached until you open the Terminal tab."}
                </>
              ) : req.verify.trim() ? (
                <>
                  <strong className="font-medium">Headless</strong>, because a
                  verify command is set. Verify decides the run&apos;s exit code
                  and an interactive session&apos;s exit code is whenever you
                  quit, so the two are refused together — clear it for a
                  console.
                </>
              ) : req.fallback.length > 0 ? (
                <>
                  <strong className="font-medium">Headless</strong>, because a
                  fallback agent is set. Routing retries a run that exited
                  non-zero having changed nothing, which is a claim about a run
                  that ends by itself — clear the chain for a console.
                </>
              ) : (
                <>
                  <strong className="font-medium">Headless</strong>, because{" "}
                  {agentMeta?.label ?? req.agent} cannot be handed a first turn
                  on the command line: its interactive argv reads a lone
                  positional as something else. Clear the prompt to attach and
                  type it in the session instead.
                </>
              )}
            </Hint>
          </div>

          <Field label="Verify command" htmlFor="verify">
            <Input
              id="verify"
              value={req.verify}
              onChange={(e) => patch({ verify: e.target.value })}
              placeholder="make test"
              className="font-mono"
            />
            <Hint>
              <>
                Wrapped around the agent&apos;s argv <em>inside</em> the
                container, and its exit code becomes the container&apos;s. In
                the container because a verify running on the host would be host
                code selected by a file the agent can write.
                {req.agent && (
                  <>
                    {" "}
                    Filling this in <em>runs the agent headless</em>: the exit
                    code is how <code>land</code> knows the work is done, and an
                    interactive session&apos;s exit code is whenever you quit.
                  </>
                )}
              </>
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
            disabled={blocked || agentsPending || launch.isPending}
          >
            <Play className="size-4" />
            {launch.isPending ? "Starting…" : "Launch sandbox"}
          </Button>
          {agentsPending && !blocked && (
            <p className="text-xs text-muted-foreground">
              Waiting for the daemon&apos;s agent list — it says whether{" "}
              {req.agent} can be handed a first turn, which is what decides the
              mode.
            </p>
          )}
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

      {/* Selecting what was just added, since adding it here means meaning to
          launch in it — and the daemon's recorded root, not the typed path,
          because the two differ whenever a subdirectory was named. */}
      <AddRepositoryDialog
        open={addRepoOpen}
        onOpenChange={setAddRepoOpen}
        onAdded={(project) =>
          patch({ repo: project.id, workspace: project.root, worktree: null })
        }
      />
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

/**
 * What a handoff actually carries, said before the launch rather than
 * discovered afterwards.
 *
 * The wording is the point. This is **not** a resume: the target is started
 * fresh and told, in its own prompt, that a previous agent stopped before
 * finishing and that its notes are mounted read-only. Describing it as
 * "continuing claude's session" would be the one claim the whole mechanism
 * exists to avoid making — an agent that believes a history is its own answers
 * as though it were, with file-writing tools.
 *
 * It says the target too, because the row that started this only chose the
 * source; the agent above can still be changed, and a briefing whose reader is
 * not what somebody expected is worth seeing before it runs.
 */
function BriefingNotice({
  from,
  to,
  onClear,
}: {
  from: { agent: string; sessionId: string };
  to: string | null;
  onClear: () => void;
}) {
  return (
    <div className="rounded-md border border-dashed bg-muted/40 p-3 text-xs">
      <div className="flex items-start justify-between gap-3">
        <div className="space-y-1">
          <p className="font-medium">
            Starting {to ?? "an agent"} with a briefing from {from.agent}
          </p>
          <p className="text-muted-foreground">
            Not a resume — {to ?? "the agent"} begins a new conversation. What
            crosses is a briefing mounted read-only at{" "}
            <code>/sandbox/context</code>: HANDOFF.md, the conversation as a
            vendor-neutral transcript, and the files that changed, derived from
            git rather than from anything {from.agent} said about itself.
          </p>
          <p className="font-mono text-[10px] text-muted-foreground">
            {from.agent} · {from.sessionId.slice(0, 8)}
          </p>
        </div>
        <Button
          variant="ghost"
          size="sm"
          className="h-6 shrink-0 px-2 text-[11px]"
          onClick={onClear}
        >
          Drop
        </Button>
      </div>
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
  headless,
}: {
  req: FormState;
  patch: (p: Partial<FormState>) => void;
  /**
   * This run has gone headless with a conversation still picked — so the daemon
   * would refuse it. The picker stays rendered to keep "clear the conversation"
   * reachable, and says which it is.
   */
  headless: boolean;
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
        onValueChange={(v) => {
          // Choosing a conversation to reopen drops one being handed over: the
          // daemon refuses the pair, and a form that could hold both would only
          // find out at 400. Choosing "none" clears the resume alone — it is not
          // a statement about the briefing.
          const next = v === "none" ? null : v;
          patch(next ? { resume: next, handoffFrom: null } : { resume: null });
        }}
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
      <Hint tone={headless ? "caution" : undefined}>
        {headless
          ? "This run is headless, and a headless resume would replay one prompt into an old conversation and exit — so the daemon refuses it. Either start a new conversation here, or clear whatever made the run headless (the line below Autonomy says which)."
          : req.resume
            ? "The prompt above is ignored: the conversation already has one, and this reopens it where it stopped."
            : "Reopens an earlier session in a fresh container. Only conversations the sandbox itself wrote are listed."}
      </Hint>
    </Field>
  );
}
