"use client";

import { useState } from "react";
import { ChevronRight, File, Folder } from "lucide-react";
import { Button } from "@/components/ui/button";
import { getToken } from "@/lib/api/client";
import { useDir } from "@/lib/api/queries";
import { formatBytes } from "@/lib/format";

/**
 * A sandbox's filesystem, read through the API: what the guest has, not your
 * machine's. It opens on the sandbox user's home, where the work happens.
 * Read-only on purpose: an editor in the browser would be a second writer to
 * a tree the agent is editing.
 */
const MAX_PREVIEW = 512 << 10;

export function SandboxFiles({ sandbox }: { sandbox: string }) {
  const [path, setPath] = useState("/sandbox/home");
  const [preview, setPreview] = useState<{ path: string; text: string } | null>(null);
  const { data, error, isLoading } = useDir(sandbox, path);
  const parts = path.split("/").filter(Boolean);

  async function open(file: string, size: number) {
    if (size > MAX_PREVIEW) {
      setPreview({ path: file, text: `(${formatBytes(size)} — too large to preview here)` });
      return;
    }
    const resp = await fetch(`/api/v1/sandboxes/${encodeURIComponent(sandbox)}/files?path=${encodeURIComponent(file)}`, {
      headers: { Authorization: `Bearer ${getToken()}` },
    });
    const buf = new Uint8Array(await resp.arrayBuffer());
    const binary = buf.subarray(0, 8000).includes(0);
    setPreview({ path: file, text: binary ? "(binary)" : new TextDecoder().decode(buf) });
  }

  return (
    <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)]">
      <div className="flex flex-col gap-2 rounded-md border p-3">
        <div className="flex flex-wrap items-center gap-1 font-mono text-xs">
          <button className="hover:underline" onClick={() => setPath("/")}>/</button>
          {parts.map((p, i) => (
            <span key={i} className="flex items-center gap-1">
              <ChevronRight className="size-3 text-muted-foreground" />
              <button className="hover:underline" onClick={() => setPath("/" + parts.slice(0, i + 1).join("/"))}>
                {p}
              </button>
            </span>
          ))}
        </div>
        {isLoading ? <p className="text-xs text-muted-foreground">reading…</p> : null}
        {error ? <p className="text-xs text-destructive">{error.message}</p> : null}
        <ul className="flex max-h-[28rem] flex-col overflow-auto text-sm">
          {path !== "/" && (
            <li>
              <Button variant="ghost" size="sm" className="w-full justify-start font-mono" onClick={() => setPath(path.split("/").slice(0, -1).join("/") || "/")}>
                ..
              </Button>
            </li>
          )}
          {[...(data ?? [])]
            .sort((a, b) => (a.type === b.type ? a.name.localeCompare(b.name) : a.type === "dir" ? -1 : 1))
            .map((e) => {
              const full = (path === "/" ? "" : path) + "/" + e.name;
              return (
                <li key={e.name}>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="w-full justify-start gap-2 font-mono"
                    onClick={() => (e.type === "dir" ? setPath(full) : open(full, e.size))}
                  >
                    {e.type === "dir" ? <Folder className="size-3.5" /> : <File className="size-3.5" />}
                    <span className="truncate">{e.name}</span>
                    {e.type === "file" ? <span className="ml-auto text-[10px] text-muted-foreground">{formatBytes(e.size)}</span> : null}
                  </Button>
                </li>
              );
            })}
        </ul>
      </div>
      <div className="flex min-w-0 flex-col gap-2 rounded-md border p-3">
        <p className="truncate font-mono text-xs text-muted-foreground">{preview?.path ?? "pick a file"}</p>
        <pre className="max-h-[28rem] overflow-auto whitespace-pre-wrap font-mono text-xs">{preview?.text ?? ""}</pre>
      </div>
    </div>
  );
}
