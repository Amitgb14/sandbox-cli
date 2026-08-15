"use client";

import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Link2, RotateCw, ShieldAlert } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { toast } from "sonner";
import { reconnect, setApiToken, apiToken } from "@/lib/api/client";
import { apiBase, setStoredApiBase, storedApiBase } from "@/lib/constants";

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
 */
export function ConnectionCard() {
  const qc = useQueryClient();
  // Read after mount: these come from localStorage, which does not exist during
  // SSR, and rendering a different value on the server than the client is a
  // hydration error on the one screen that prints the endpoint.
  const [endpoint, setEndpoint] = useState("");
  const [token, setToken] = useState("");
  const [custom, setCustom] = useState(false);
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
              onChange={(e) => setEndpoint(e.target.value)}
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
            <Input
              id="token"
              type="password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && apply(endpoint, token)}
              placeholder="the token that daemon printed"
              className="font-mono text-xs"
              spellCheck={false}
              autoComplete="off"
            />
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
        </div>

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
