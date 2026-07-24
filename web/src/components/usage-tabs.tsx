"use client";

import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { CopyButton } from "@/components/copy-button";

interface Snippet {
  key: string;
  label: string;
  code: string;
}

const SNIPPETS: Snippet[] = [
  {
    key: "install",
    label: "install",
    code: `# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/Aegmis/sandbox-cli/main/install.sh | sh

# …or from source (Go 1.25+)
make install

# optional: scaffold a per-project config
sandbox-cli init`,
  },
  {
    key: "run",
    label: "run anything",
    code: `# sandbox flags first, your command after --
sandbox-cli run -- bash
sandbox-cli run -- npm test

# see for yourself: fake HOME, one mount
sandbox-cli run -- sh -c 'echo $HOME; ls ~; ls /workspace'

# print the docker command instead of running it
sandbox-cli run --dry-run -- npm test`,
  },
  {
    key: "agents",
    label: "run an agent",
    code: `# keys forwarded from your env only when set
ANTHROPIC_API_KEY=... sandbox-cli claude
OPENAI_API_KEY=...    sandbox-cli codex exec 'run the tests'
GEMINI_API_KEY=...    sandbox-cli gemini --yolo

# the flag everyone's nervous about — now boring
ANTHROPIC_API_KEY=... sandbox-cli claude --dangerously-skip-permissions

# everything after the agent name forwards verbatim`,
  },
  {
    key: "parallel",
    label: "go parallel",
    code: `# each agent gets its own branch, worktree and sandbox
sandbox-cli claude --worktree feature-a -- -p "implement A"
sandbox-cli codex  --worktree feature-b

# inspect and clean up
sandbox-cli worktree list
sandbox-cli worktree rm feature-a

# live CPU / memory gauge in a second terminal
sandbox-cli stats`,
  },
];

/** Minimal shell-ish colouring: comments dimmed, flags in signal, command heads teal. */
function highlight(line: string) {
  if (line.trimStart().startsWith("#")) {
    return <span className="italic text-muted-foreground/75">{line}</span>;
  }
  return (
    <>
      {line.split(/(\s+)/).map((tok, i) => {
        if (tok.startsWith("--") || /^-[a-z]$/.test(tok)) {
          return (
            <span key={i} className="text-signal">
              {tok}
            </span>
          );
        }
        if (tok === "sandbox-cli" || tok === "make" || tok === "curl") {
          return (
            <span key={i} className="font-semibold text-contained">
              {tok}
            </span>
          );
        }
        if (/^[A-Z_]+=/.test(tok)) {
          return (
            <span key={i} className="text-exposed">
              {tok}
            </span>
          );
        }
        return <span key={i}>{tok}</span>;
      })}
    </>
  );
}

export function UsageTabs() {
  return (
    <Tabs defaultValue="install" className="overflow-hidden rounded-xl border bg-card shadow-sm">
      <TabsList className="w-full justify-start rounded-none border-b bg-muted/50 p-1.5">
        {SNIPPETS.map((s) => (
          <TabsTrigger key={s.key} value={s.key} className="font-mono text-xs">
            {s.label}
          </TabsTrigger>
        ))}
      </TabsList>

      {SNIPPETS.map((s) => (
        <TabsContent key={s.key} value={s.key} className="relative m-0">
          <div className="absolute right-3 top-3 z-10">
            <CopyButton value={s.code} />
          </div>
          <pre className="no-scrollbar overflow-x-auto p-6 font-mono text-[0.82rem] leading-[1.9]">
            {s.code.split("\n").map((line, i) => (
              <span key={i} className="block">
                {highlight(line)}
              </span>
            ))}
          </pre>
        </TabsContent>
      ))}
    </Tabs>
  );
}
