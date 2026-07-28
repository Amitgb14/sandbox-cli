"use client";

import { useState } from "react";
import { AlertTriangle, ShieldCheck } from "lucide-react";
import { DEPLOY_MODES, PROD_ASSERTIONS, PROFILE_MATRIX } from "@/lib/deploy";
import { CopyButton } from "@/components/copy-button";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

/**
 * The two deployments, as steps you can follow rather than a table of settings.
 *
 * The matrix stays visible while you read either path, with the selected mode's
 * column lit: the point of the section is the *difference*, and hiding half of a
 * comparison behind the tab you are not on makes it a spec sheet instead.
 */
export function DeployGuide() {
  const [active, setActive] = useState(DEPLOY_MODES[0].id);
  const mode = DEPLOY_MODES.find((m) => m.id === active) ?? DEPLOY_MODES[0];
  const isProd = mode.id === "prod";

  return (
    <div className="flex flex-col gap-5">
      <div
        role="tablist"
        aria-label="Deployment"
        className="grid grid-cols-1 gap-1.5 rounded-xl border bg-card p-1.5 sm:grid-cols-2"
      >
        {DEPLOY_MODES.map((m) => (
          <button
            key={m.id}
            role="tab"
            aria-selected={m.id === active}
            onClick={() => setActive(m.id)}
            className={cn(
              "flex flex-col items-start gap-0.5 rounded-lg px-3.5 py-2.5 text-left transition-colors",
              m.id === active
                ? "bg-foreground text-background"
                : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
            )}
          >
            <span className="text-[0.88rem] font-medium">{m.label}</span>
            <span
              className={cn(
                "font-mono text-[0.65rem]",
                m.id === active ? "text-background/70" : "text-muted-foreground/70",
              )}
            >
              {m.flag}
            </span>
          </button>
        ))}
      </div>

      <div className="rounded-xl border bg-card px-4 py-3.5">
        <p className="eyebrow mb-1.5">{mode.tagline}</p>
        <p className="text-[0.85rem] leading-relaxed text-muted-foreground">{mode.summary}</p>
      </div>

      {/* ------------------------------------------------- what differs */}
      <div className="overflow-hidden rounded-2xl border bg-card">
        <div className="overflow-x-auto">
          <table className="w-full min-w-[38rem] border-collapse text-left">
            <thead>
              <tr className="border-b">
                <th className="px-4 py-3 text-[0.8rem] font-medium sm:px-5">
                  Within the same host boundary
                </th>
                {DEPLOY_MODES.map((m) => (
                  <th
                    key={m.id}
                    className={cn(
                      "px-4 py-3 text-[0.8rem] font-medium",
                      m.id === active ? "bg-muted/50" : "text-muted-foreground",
                    )}
                  >
                    {m.label}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {PROFILE_MATRIX.map((row) => (
                <tr key={row.setting} className="border-b last:border-0">
                  <td className="px-4 py-3 align-top sm:px-5">
                    <span className="block text-[0.8rem]">{row.setting}</span>
                    {row.note ? (
                      <span className="mt-1 block max-w-md text-[0.7rem] leading-relaxed text-muted-foreground">
                        {row.note}
                      </span>
                    ) : null}
                  </td>
                  <td
                    className={cn(
                      "px-4 py-3 align-top text-[0.78rem] text-muted-foreground",
                      active === "dev" && "bg-muted/50 text-foreground",
                    )}
                  >
                    {row.dev}
                  </td>
                  <td
                    className={cn(
                      "px-4 py-3 align-top text-[0.78rem] text-muted-foreground",
                      active === "prod" && "bg-muted/50 text-foreground",
                    )}
                  >
                    {row.prod}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {/* -------------------------------------------------------- steps */}
      <ol className="flex flex-col gap-3">
        {mode.steps.map((s, i) => (
          <li key={s.title} className="rounded-xl border bg-card px-4 py-3.5">
            <div className="flex items-baseline gap-2.5">
              <Badge
                variant="outline"
                className="shrink-0 border-border font-mono text-[0.62rem] font-normal text-muted-foreground"
              >
                {i + 1}
              </Badge>
              <h4 className="text-[0.88rem] font-medium">{s.title}</h4>
            </div>

            {s.warn ? (
              <div className="mt-2.5 flex items-start gap-2.5 rounded-lg border border-caution/40 bg-caution/5 px-3 py-2">
                <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-caution" />
                <p className="text-[0.78rem] leading-relaxed text-foreground">{s.warn}</p>
              </div>
            ) : null}

            {s.code ? (
              <div className="group relative mt-2.5 overflow-x-auto rounded-lg border bg-muted/40 px-3.5 py-2.5">
                <pre className="font-mono text-[0.72rem] leading-relaxed whitespace-pre">
                  {s.code}
                </pre>
                <div className="absolute top-2 right-2 opacity-0 transition-opacity group-hover:opacity-100">
                  <CopyButton value={s.code} />
                </div>
              </div>
            ) : null}

            <p className="mt-2.5 text-[0.8rem] leading-relaxed text-muted-foreground">{s.body}</p>
          </li>
        ))}
      </ol>

      {/* -------------------------------------- what prod asserts at run time */}
      {isProd ? (
        <div className="rounded-2xl border border-contained-line bg-contained-soft/40 px-4 py-3.5 sm:px-5">
          <div className="flex items-center gap-2">
            <ShieldCheck className="size-4 text-contained" />
            <h4 className="text-[0.88rem] font-medium">
              Checked against the configuration that will actually run
            </h4>
          </div>
          <p className="mt-2 text-[0.8rem] leading-relaxed text-muted-foreground">
            Prod is a base layer your own config can build on, so it has to be verified after every
            layer is merged rather than assumed from the flag. If any of these no longer holds, the
            run stops and names the ones that failed.
          </p>
          <ul className="mt-3 grid grid-cols-1 gap-1.5 sm:grid-cols-2">
            {PROD_ASSERTIONS.map((a) => (
              <li
                key={a}
                className="rounded-lg border bg-card px-3 py-2 font-mono text-[0.7rem] text-muted-foreground"
              >
                {a}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  );
}
