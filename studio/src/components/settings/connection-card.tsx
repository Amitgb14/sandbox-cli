"use client";

import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Eye, EyeOff, Link2, RotateCw, ShieldAlert, Star, Trash2 } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { toast } from "sonner";
import { reconnect, setApiToken, apiToken } from "@/lib/api/client";
import { apiBase, setStoredApiBase, storedApiBase } from "@/lib/constants";
import { useUi } from "@/lib/store";

/**
 * Point this Studio at a daemon on another machine.
 *
 * The case it exists for: sandbox-cli and its containers run on a Linux box, and
 * the browser is here. Nothing about the container boundary changes — every
 * safety refusal is still evaluated on the machine sandbox-cli runs on, which is
 * why "remote" has to mean *the whole daemon is remote* rather than a local
 * daemon pointed at a remote docker.
 *
 * Both fields live in localStorage rather than in the served page, because the
 * whole point is reaching a machine the local server knows nothing about. A
 * typed endpoint therefore outranks the injected one — the opposite of the
 * token's precedence, and for a reason: an injected token is *this* server
 * saying what it is running with, while an injected URL is only a default
 * location.
 *
 * The token follows the endpoint. Pointing at another daemon means the injected
 * token belongs to the wrong machine, so once an endpoint is set the stored
 * token is the one used.
 *
 * The saved list is an address book beside those two, not a replacement for
 * them: the active connection stays in localStorage, because it is read while a
 * request is being built rather than while a component renders. Each entry
 * carries a URL *and* its token, since a token belongs to exactly one machine —
 * a list of URLs alone would ask for the token again on every switch, which is
 * the paste this exists to remove.
 */
export function ConnectionCard() {
  const qc = useQueryClient();
  // Read after mount: these come from localStorage, which does not exist during
  // SSR, and rendering a different value on the server than the client is a
  // hydration error on the one screen that prints the endpoint.
  const [endpoint, setEndpoint] = useState("");
  const [token, setToken] = useState("");
  const [custom, setCustom] = useState(false);
  // Never persisted and reset on every mount: revealing is for the moment you
  // are typing, not a preference that leaves a token on screen for whoever
  // walks past next.
  const [shown, setShown] = useState(false);
  const saved = useUi((s) => s.connections);
  const save = useUi((s) => s.saveConnection);
  const forget = useUi((s) => s.forgetConnection);
  const [effective, setEffective] = useState("");

  useEffect(() => {
    setEndpoint(storedApiBase());
    setToken(apiToken());
    setCustom(!!storedApiBase());
    setEffective(apiBase());
  }, []);

  function apply(nextEndpoint: string, nextToken: string) {
    const url = nextEndpoint.trim().replace(/\/+$/, "");
    if (url && !/^https?:\/\//.test(url)) {
      toast.error("Give a full URL", {
        description: "It has to start with http:// or https:// — the browser dials this, so a bare host is not enough.",
      });
      return;
    }
    setStoredApiBase(url);
    setApiToken(nextToken.trim());
    setCustom(!!url);
    setEffective(apiBase());

    // Everything on screen came from the old daemon, so the cached answers are
    // now about a different machine. Forget the probe and refetch rather than
    // leaving one host's runs under another's name.
    reconnect();
    qc.invalidateQueries();
    toast.success(url ? `Connecting to ${url}` : "Back to this machine's daemon");
  }

  return (
    <Card className="surface-sheen gap-4">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-sm font-medium">
          <Link2 className="size-4 text-muted-foreground" />
          Connection
          {custom && (
            <Badge variant="outline" className="text-[10px]">
              remote
            </Badge>
          )}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="text-xs text-muted-foreground">
          Where this browser looks for a daemon. Leave it empty to use the one this Studio
          was started beside{effective ? ` (${effective})` : ""}.
        </p>

        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label htmlFor="endpoint" className="text-xs">
              Daemon URL
            </Label>
            <Input
              id="endpoint"
              value={endpoint}
              onChange={(e) => {
                // Changing the machine clears the token, because a token belongs
                // to exactly one daemon. Without this the common flow — connected
                // locally, so the box is pre-filled with *this* machine's token,
                // then type a remote URL and press Save — stored the local token
                // against the remote URL, and connecting to that entry would send
                // one machine's bearer token to another. Applying both halves
                // together cannot help when both halves were wrong.
                if (e.target.value.trim() !== endpoint.trim()) setToken("");
                setEndpoint(e.target.value);
              }}
              onKeyDown={(e) => e.key === "Enter" && apply(endpoint, token)}
              placeholder="http://10.0.0.5:8787"
              className="font-mono text-xs"
              spellCheck={false}
              autoComplete="off"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="token" className="text-xs">
              Token
            </Label>
            <div className="relative">
              <Input
                id="token"
                type={shown ? "text" : "password"}
                value={token}
                onChange={(e) => setToken(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && apply(endpoint, token)}
                placeholder="the token that daemon printed"
                className="pr-9 font-mono text-xs"
                spellCheck={false}
                autoComplete="off"
              />
              {/* Revealable, because this field is *typed into* rather than
                  merely stored: a token pasted from another machine's terminal
                  is checked by reading it back, and a masked field turns one
                  wrong character into "the daemon is unreachable".

                  It reveals rather than copies. A copy button would put a
                  bearer token on the system clipboard, where the next paste
                  anywhere sends it — a worse trade for a value whose whole job
                  is to be secret, and one this card cannot undo. */}
              <Button
                type="button"
                variant="ghost"
                size="icon"
                onClick={() => setShown((v) => !v)}
                className="absolute right-0 top-0 size-9 text-muted-foreground hover:text-foreground"
                aria-label={shown ? "Hide the token" : "Show the token"}
                title={shown ? "Hide" : "Show"}
              >
                {shown ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
              </Button>
            </div>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" onClick={() => apply(endpoint, token)}>
            <RotateCw className="size-3.5" />
            Connect
          </Button>
          {custom && (
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                setEndpoint("");
                apply("", token);
              }}
            >
              Use this machine
            </Button>
          )}
          {/* Saving is separate from connecting on purpose: the pair you are
              typing is often the one that does not work yet, and a list that
              filled itself from every attempt would collect the typos. */}
          <Button
            size="sm"
            variant="ghost"
            // A URL with no token is not a connection anyone can use, and saving
            // one would put a row in the list that fails the moment it is
            // clicked.
            disabled={!endpoint.trim() || !token.trim()}
            onClick={() => {
              const url = endpoint.trim().replace(/\/+$/, "");
              save({ label: labelFor(url), url, token: token.trim() });
              toast.success(`Saved ${labelFor(url)}`);
            }}
          >
            <Star className="size-3.5" />
            Save
          </Button>
        </div>

        {saved.length > 0 && (
          <div className="space-y-1.5">
            <Label className="text-xs">Saved daemons</Label>
            <ul className="divide-y rounded-md border">
              {saved.map((c) => {
                const active = c.url === effective;
                return (
                  <li key={c.url} className="flex items-center gap-2 px-2.5 py-2">
                    <span className="min-w-0 flex-1 truncate">
                      <span className="font-mono text-xs">{c.label}</span>
                      <span className="ml-2 font-mono text-[10px] text-muted-foreground">
                        {c.url}
                      </span>
                    </span>
                    {active ? (
                      <Badge variant="outline" className="text-[10px]">
                        connected
                      </Badge>
                    ) : (
                      <Button
                        size="sm"
                        variant="ghost"
                        className="h-7 text-xs"
                        onClick={() => {
                          // Both halves together: a token belongs to one machine,
                          // so carrying the old one to a new URL is the failure
                          // this list exists to remove.
                          setEndpoint(c.url);
                          setToken(c.token);
                          apply(c.url, c.token);
                        }}
                      >
                        Connect
                      </Button>
                    )}
                    <Button
                      size="icon"
                      variant="ghost"
                      className="size-7 text-muted-foreground hover:text-destructive"
                      title="Forget this daemon"
                      aria-label={`Forget ${c.label}`}
                      onClick={() => forget(c.url)}
                    >
                      <Trash2 className="size-3.5" />
                    </Button>
                  </li>
                );
              })}
            </ul>
            <p className="text-[11px] text-muted-foreground">
              Stored in this browser, tokens included — the same place the active
              one already lives, so this adds reach rather than exposure. Forget
              an entry to remove it; it does not touch the daemon.
            </p>
          </div>
        )}

        {/* Said where the decision is made, not in a document nobody opens. The
            daemon holds the docker socket, so this is not a detail about
            convenience. */}
        {custom && (
          <div className="flex items-start gap-2 rounded-md border border-caution/30 bg-caution/5 p-2.5 text-xs">
            <ShieldAlert className="mt-0.5 size-3.5 shrink-0 text-caution" />
            <p className="text-muted-foreground">
              There is no TLS here: this token and everything it protects cross the network in
              cleartext. Over anything but a private network, run the daemon on loopback and
              reach it through a tunnel —{" "}
              <code className="font-mono">ssh -N -L 8787:127.0.0.1:8787 you@box</code> — then
              point this at <code className="font-mono">http://localhost:8787</code>. The
              daemon must also be started with{" "}
              <code className="font-mono">-allow-host</code> for the address you typed and{" "}
              <code className="font-mono">-cors-origin</code> for this page&apos;s origin, or
              it will refuse the request and say so.
            </p>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

/**
 * What to call a daemon in the list: its host, which is what actually
 * distinguishes two of them. The port joins in only when there are two on one
 * host, which is exactly the case a bare hostname would render ambiguous.
 */
function labelFor(url: string): string {
  try {
    const u = new URL(url);
    return u.port && u.port !== "80" && u.port !== "443" ? `${u.hostname}:${u.port}` : u.hostname;
  } catch {
    return url;
  }
}
