"use client";

import { useMemo, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { ShieldAlert, ShieldCheck } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { CopyButton } from "@/components/copy-button";
import { cn } from "@/lib/utils";

type AgentKey = "run" | "claude" | "codex" | "aider";

const TARGETS: Record<AgentKey, { cli: string; label: string; guest: string; home: string | null; env: string[] }> = {
  run: { cli: "run", label: "run — npm test", guest: "npm test", home: null, env: [] },
  claude: { cli: "claude", label: "claude", guest: "claude", home: "claude", env: ["ANTHROPIC_API_KEY"] },
  codex: { cli: "codex", label: "codex", guest: "codex", home: "codex", env: ["OPENAI_API_KEY"] },
  aider: { cli: "aider", label: "aider", guest: "aider", home: "aider", env: ["OPENAI_API_KEY", "ANTHROPIC_API_KEY"] },
};

interface Line {
  id: string;
  parts: { text: string; tone?: "flag" | "path" | "danger" | "img" | "cmd" }[];
}

export function DryRunBuilder() {
  const [agent, setAgent] = useState<AgentKey>("run");
  const [worktree, setWorktree] = useState(false);
  const [branch, setBranch] = useState("feature-a");
  const [allow, setAllow] = useState(false);
  const [domain, setDomain] = useState("api.anthropic.com");
  const [noPersist, setNoPersist] = useState(false);
  const [asRoot, setAsRoot] = useState(false);

  const t = TARGETS[agent];

  const lines = useMemo<Line[]>(() => {
    const src = worktree
      ? `~/.config/sandbox/worktrees/myapp/${branch || "feature-a"}`
      : "~/projects/myapp";

    const out: Line[] = [
      { id: "run", parts: [{ text: "docker", tone: "cmd" }, { text: " run " }, { text: "--rm -it", tone: "flag" }, { text: " \\" }] },
      { id: "mount", parts: [{ text: "  -v ", tone: "flag" }, { text: src, tone: "path" }, { text: ":" }, { text: "/workspace", tone: "path" }, { text: " \\" }] },
      { id: "wd", parts: [{ text: "  -w ", tone: "flag" }, { text: "/workspace" }, { text: " \\" }] },
      { id: "home", parts: [{ text: "  -e ", tone: "flag" }, { text: "HOME=/sandbox/home" }, { text: " \\" }] },
    ];

    if (t.home && !noPersist) {
      out.push({
        id: "agenthome",
        parts: [
          { text: "  -v ", tone: "flag" },
          { text: `~/.config/sandbox/agents/${t.home}`, tone: "path" },
          { text: ":" },
          { text: "/sandbox/home", tone: "path" },
          { text: " \\" },
        ],
      });
    }

    for (const e of t.env) {
      out.push({
        id: `env-${e}`,
        parts: [{ text: "  -e ", tone: "flag" }, { text: `${e}=` }, { text: `$${e}`, tone: "img" }, { text: " \\" }],
      });
    }

    if (allow) {
      out.push({ id: "net", parts: [{ text: "  --network ", tone: "flag" }, { text: "sandbox-egress" }, { text: " \\" }] });
      out.push({
        id: "allow",
        parts: [{ text: "  -e ", tone: "flag" }, { text: "SANDBOX_ALLOW=" }, { text: domain || "api.anthropic.com", tone: "path" }, { text: " \\" }],
      });
    }

    out.push(
      asRoot
        ? { id: "user", parts: [{ text: "  « --user omitted: running as root »", tone: "danger" }, { text: " \\" }] }
        : { id: "user", parts: [{ text: "  --user ", tone: "flag" }, { text: "sandbox" }, { text: " \\" }] },
    );

    out.push({ id: "img", parts: [{ text: "  sandbox-base:0.0.1", tone: "img" }, { text: " \\" }] });
    out.push({ id: "guest", parts: [{ text: `  ${t.guest}`, tone: "cmd" }] });

    return out;
  }, [t, worktree, branch, allow, domain, noPersist, asRoot]);

  const cli = useMemo(() => {
    const flags: string[] = [];
    if (worktree) flags.push(`--worktree ${branch || "feature-a"}`);
    if (allow) flags.push(`--allow ${domain || "api.anthropic.com"}`);
    if (noPersist) flags.push("--no-persist-auth");
    if (asRoot) flags.push("--root");
    const tail = agent === "run" ? " -- npm test" : "";
    return `sandbox-cli ${t.cli} ${[...flags, "--dry-run"].join(" ")}${tail}`;
  }, [agent, t.cli, worktree, branch, allow, domain, noPersist, asRoot]);

  const plain = useMemo(
    () => lines.map((l) => l.parts.map((p) => p.text).join("")).join("\n"),
    [lines],
  );

  const verdict = asRoot
    ? { warn: true, text: "Root inside the box — still only /workspace is mounted, but drop this if you can." }
    : allow
      ? { warn: false, text: "One host path mounted, egress pinned to one domain. Tightest setting." }
      : worktree
        ? { warn: false, text: "Mounts an isolated worktree — the main checkout is untouched." }
        : noPersist
          ? { warn: false, text: "No saved login mounted. The agent starts logged out every run." }
          : { warn: false, text: "One host path mounted. Home directory unreachable." };

  return (
    <div className="grid overflow-hidden rounded-xl border bg-card shadow-xl md:grid-cols-[320px_1fr]">
      {/* controls */}
      <div className="flex flex-col gap-5 border-b bg-muted/30 p-5 md:border-b-0 md:border-r">
        <div className="flex flex-col gap-2">
          <span className="font-mono text-[0.68rem] uppercase tracking-[0.14em] text-muted-foreground">
            What to run
          </span>
          <div className="flex flex-wrap gap-1.5">
            {(Object.keys(TARGETS) as AgentKey[]).map((k) => (
              <button
                key={k}
                onClick={() => setAgent(k)}
                aria-pressed={agent === k}
                className={cn(
                  "rounded-md border px-2.5 py-1.5 font-mono text-xs transition-colors",
                  agent === k
                    ? "border-contained bg-contained font-semibold text-background"
                    : "bg-card text-muted-foreground hover:border-contained/40 hover:text-foreground",
                )}
              >
                {TARGETS[k].label}
              </button>
            ))}
          </div>
        </div>

        <div className="flex flex-col gap-1">
          <span className="font-mono text-[0.68rem] uppercase tracking-[0.14em] text-muted-foreground">
            Sandbox flags
          </span>

          <Flag name="--worktree" note="isolate on its own branch" checked={worktree} onChange={setWorktree} />
          <Input
            value={branch}
            onChange={(e) => setBranch(e.target.value)}
            disabled={!worktree}
            aria-label="Worktree branch name"
            className="h-8 font-mono text-xs"
          />

          <Flag name="--allow" note="restrict network egress" checked={allow} onChange={setAllow} />
          <Input
            value={domain}
            onChange={(e) => setDomain(e.target.value)}
            disabled={!allow}
            aria-label="Allowed domain"
            className="h-8 font-mono text-xs"
          />

          <Flag
            name="--no-persist-auth"
            note="drop the saved agent login"
            checked={noPersist}
            onChange={setNoPersist}
            disabled={!t.home}
          />
          <Flag name="--root" note="run as root inside the box" checked={asRoot} onChange={setAsRoot} danger />
        </div>
      </div>

      {/* output */}
      <div className="flex flex-col">
        <div className="flex items-center gap-2 border-b px-4 py-2.5 font-mono text-xs text-muted-foreground">
          <span className="no-scrollbar overflow-x-auto whitespace-nowrap">$ {cli}</span>
          <span className="flex-1" />
          <CopyButton value={plain} label="Copy" />
        </div>

        <pre className="no-scrollbar flex-1 overflow-x-auto p-5 font-mono text-[0.8rem] leading-[1.95]">
          <AnimatePresence initial={false} mode="popLayout">
            {lines.map((l) => (
              <motion.span
                key={l.id}
                layout
                initial={{ opacity: 0, x: -8 }}
                animate={{ opacity: 1, x: 0 }}
                exit={{ opacity: 0, x: -8 }}
                transition={{ duration: 0.22 }}
                className="block"
              >
                {l.parts.map((p, i) => (
                  <span
                    key={i}
                    className={cn(
                      p.tone === "flag" && "text-signal",
                      p.tone === "path" && "text-contained",
                      p.tone === "danger" && "font-semibold text-exposed",
                      p.tone === "img" && "text-muted-foreground",
                      p.tone === "cmd" && "font-semibold text-foreground",
                    )}
                  >
                    {p.text}
                  </span>
                ))}
              </motion.span>
            ))}
          </AnimatePresence>
        </pre>

        <div
          className={cn(
            "flex items-center gap-2 border-t bg-muted/30 px-5 py-3 font-mono text-xs",
            verdict.warn ? "text-exposed" : "text-contained",
          )}
        >
          {verdict.warn ? <ShieldAlert className="size-4 shrink-0" /> : <ShieldCheck className="size-4 shrink-0" />}
          <span>{verdict.text}</span>
        </div>
      </div>
    </div>
  );
}

function Flag({
  name,
  note,
  checked,
  onChange,
  danger,
  disabled,
}: {
  name: string;
  note: string;
  checked: boolean;
  onChange: (v: boolean) => void;
  danger?: boolean;
  disabled?: boolean;
}) {
  return (
    <label className={cn("flex items-center justify-between gap-3 py-2", disabled && "opacity-45")}>
      <span className="flex flex-col">
        <span className="font-mono text-xs">{name}</span>
        <span className="text-[0.7rem] text-muted-foreground">{note}</span>
      </span>
      <Switch
        checked={checked}
        onCheckedChange={onChange}
        disabled={disabled}
        aria-label={name}
        className={danger ? "data-[checked]:bg-exposed" : "data-[checked]:bg-contained"}
      />
    </label>
  );
}
