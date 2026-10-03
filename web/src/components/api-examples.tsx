"use client";

import { useState } from "react";
import { CodeBlock } from "@/components/code-block";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { cn } from "@/lib/utils";

/**
 * The same task — make a sandbox with no network, run a command, read its
 * output, throw the sandbox away — in each client. The point is that they are
 * the same: four spellings of one API, not four products. Each snippet is the
 * real call with the real field names (docs/api/v1.md, sdk/python,
 * sdk/typescript), not pseudocode.
 */
const EXAMPLES = [
  {
    id: "cli",
    label: "CLI",
    lang: "sh" as const,
    code: `sandbox-cli run --network none -- echo hello
# hello`,
  },
  {
    id: "curl",
    label: "curl",
    lang: "sh" as const,
    code: `SOCK=$XDG_RUNTIME_DIR/sandboxd.sock
ID=$(curl -s --unix-socket "$SOCK" -X POST http://localhost/v1/sandboxes \\
  -H 'Content-Type: application/json' -d '{"network":{"mode":"none"}}' | jq -r .id)
curl -s --unix-socket "$SOCK" -X POST http://localhost/v1/sandboxes/$ID/run \\
  -H 'Content-Type: application/json' -d '{"argv":["echo","hello"]}' \\
  | jq -r .stdout | base64 -d
curl -s --unix-socket "$SOCK" -X DELETE http://localhost/v1/sandboxes/$ID`,
  },
  {
    id: "python",
    label: "Python",
    lang: "code" as const,
    code: `from sandboxapi import Client

c = Client("unix:///run/user/1000/sandboxd.sock")   # or https://box:7443, token=…
sb = c.create_sandbox(network={"mode": "none"})
print(c.run(sb["id"], ["echo", "hello"])["stdout"])  # b'hello\\n'
c.terminate_sandbox(sb["id"])`,
  },
  {
    id: "typescript",
    label: "TypeScript",
    lang: "code" as const,
    code: `import { Client } from "./sdk/typescript/src";

const c = new Client("https://box.example.internal:7443", { token });
const sb = await c.createSandbox({ network: { mode: "none" } });
const res = await c.run(sb.id, ["echo", "hello"]);
console.log(new TextDecoder().decode(res.stdout)); // "hello\\n"
await c.terminateSandbox(sb.id);`,
  },
];

export function ApiExamples({ className }: { className?: string }) {
  const [active, setActive] = useState(EXAMPLES[0].id);
  const ex = EXAMPLES.find((e) => e.id === active) ?? EXAMPLES[0];
  return (
    <div className={cn("flex flex-col gap-3", className)}>
      <Tabs value={active} onValueChange={(v) => setActive(String(v))} className="gap-0">
        <TabsList variant="line" className="h-9 gap-0.5">
          {EXAMPLES.map((e) => (
            <TabsTrigger key={e.id} value={e.id} className="px-2.5 text-[0.8rem]">
              {e.label}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>
      <CodeBlock code={ex.code} lang={ex.lang} />
    </div>
  );
}
