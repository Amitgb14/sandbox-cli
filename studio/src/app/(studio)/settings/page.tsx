"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useTheme } from "next-themes";
import {
  Cpu,
  ExternalLink,
  Globe,
  Monitor,
  PlugZap,
  ShieldCheck,
  Terminal,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import { Switch } from "@/components/ui/switch";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { PageHeader } from "@/components/common/page-header";
import { apiBase, BASELINE_EGRESS, PROFILES } from "@/lib/constants";
import { ConnectionCard } from "@/components/settings/connection-card";
import { RepositoriesCard } from "@/components/settings/repositories-card";
import { SnapshotsCard } from "@/components/settings/snapshots-card";
import { SnapshotStorageCard } from "@/components/settings/snapshot-storage-card";
import { useDaemon, useTransportMode } from "@/lib/api/queries";
import { useUi } from "@/lib/store";
import { formatBytes } from "@/lib/format";

/**
 * Settings.
 *
 * A deliberate limit: **the security-relevant configuration is read-only here.**
 * The profile, the egress posture and the mount rules are decided by the config
 * layers — built-in default, profile, your config, the project's `.sandbox.yaml`,
 * an explicit `--config`, flags — and a UI that wrote into the middle of that
 * stack would become a layer nobody could see in the file. What Studio owns is
 * what Studio renders; everything else it explains and links to.
 */
export default function SettingsPage() {
  const { theme, setTheme } = useTheme();

  // The resolved theme is not knowable until the browser has read where it was
  // stored, so the server has to guess and guesses "dark" — while a user who
  // chose light hydrates with "light". That difference lands on `data-state` and
  // `aria-checked` of the two toggle buttons, which React reports as a mismatch
  // it will not patch up.
  //
  // Rendering no selection until mounted is what makes both first passes agree.
  // The buttons are still drawn, so nothing shifts; only the highlight arrives a
  // frame late. Every other theme consumer in the app is safe already, because
  // they read `theme` inside a click handler or express the state as a `dark:`
  // class, and neither is an attribute in the server's HTML.
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);

  const { data: daemon } = useDaemon();
  const { mode, retry } = useTransportMode();

  const terminalWrap = useUi((s) => s.terminalWrap);
  const setTerminalWrap = useUi((s) => s.setTerminalWrap);
  const terminalTimestamps = useUi((s) => s.terminalTimestamps);
  const setTerminalTimestamps = useUi((s) => s.setTerminalTimestamps);
  const terminalFollow = useUi((s) => s.terminalFollow);
  const setTerminalFollow = useUi((s) => s.setTerminalFollow);
  const diffView = useUi((s) => s.diffView);
  const setDiffView = useUi((s) => s.setDiffView);
  const usageHidden = useUi((s) => s.usageHidden);
  const setUsageHidden = useUi((s) => s.setUsageHidden);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Settings"
        description="What Studio itself remembers, and what the CLI decides. The second group is read-only on purpose."
        actions={
          <Button asChild variant="outline" size="sm">
            <Link href="/settings/doctor">
              <ShieldCheck className="size-4" />
              Run doctor
            </Link>
          </Button>
        }
      />

      <div className="grid gap-4 lg:grid-cols-2">
        <Card className="surface-sheen gap-4">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-sm font-medium">
              <Monitor className="size-4 text-muted-foreground" />
              Appearance
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-1.5">
              <Label className="text-xs">Theme</Label>
              <ToggleGroup
                type="single"
                variant="outline"
                size="sm"
                value={mounted ? (theme ?? "dark") : ""}
                onValueChange={(v) => v && setTheme(v)}
              >
                <ToggleGroupItem value="dark" className="px-3 text-xs">
                  Dark
                </ToggleGroupItem>
                <ToggleGroupItem value="light" className="px-3 text-xs">
                  Light
                </ToggleGroupItem>
              </ToggleGroup>
              <p className="text-xs text-muted-foreground">
                Dark is the designed mode. Light is stepped for its own surface and validated
                against it — not an automatic flip of the dark one.
              </p>
            </div>

            <Separator />

            <div className="space-y-1.5">
              <Label className="text-xs">Default diff view</Label>
              <ToggleGroup
                type="single"
                variant="outline"
                size="sm"
                value={diffView}
                onValueChange={(v) => v && setDiffView(v as "unified" | "split")}
              >
                <ToggleGroupItem value="unified" className="px-3 text-xs">
                  Unified
                </ToggleGroupItem>
                <ToggleGroupItem value="split" className="px-3 text-xs">
                  Split
                </ToggleGroupItem>
              </ToggleGroup>
            </div>
          </CardContent>
        </Card>

        <Card className="surface-sheen gap-4">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-sm font-medium">
              <Terminal className="size-4 text-muted-foreground" />
              Terminal
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <Row
              id="wrap"
              checked={terminalWrap}
              onChange={setTerminalWrap}
              label="Wrap long lines"
              hint="Off keeps an agent's alignment intact at the cost of horizontal scrolling."
            />
            <Row
              id="ts"
              checked={terminalTimestamps}
              onChange={setTerminalTimestamps}
              label="Show timestamps"
              hint="Useful when you are working out where an agent spent its time."
            />
            <Row
              id="follow"
              checked={terminalFollow}
              onChange={setTerminalFollow}
              label="Follow new output by default"
              hint="Scrolling up always turns this off for the session you are reading — a view that yanked you back to the bottom mid-read would be unreadable."
            />

            <Separator />

            {/* Not in the security card below, which is read-only on purpose:
                this is Studio's own chrome, which is exactly what Studio owns.

                Off by default, so this row is where the panel is turned *on*
                rather than where it is turned off. */}
            <Row
              id="usage-panel"
              checked={!usageHidden}
              onChange={(v) => setUsageHidden(!v)}
              label="Show subscription usage"
              hint="The sidebar gauge, off by default. Live on any machine where a sandboxed claude has run interactively — the status line records the figures as it goes. Leaving it off also stops the request behind it."
            />
          </CardContent>
        </Card>
      </div>

      <Card className="surface-sheen gap-4">
        <CardHeader className="gap-1">
          <CardTitle className="flex items-center gap-2 text-sm font-medium">
            <ShieldCheck className="size-4 text-muted-foreground" />
            Security posture
            <Badge variant="outline" className="text-[10px]">
              read-only
            </Badge>
          </CardTitle>
          <p className="text-xs text-muted-foreground">
            Decided by the config layers, not by Studio. Precedence, later wins: built-in default →
            profile → your <code className="font-mono">~/.config/sandbox/config.yaml</code> → the
            nearest <code className="font-mono">.sandbox.yaml</code> → an explicit{" "}
            <code className="font-mono">--config</code> → flags.
          </p>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid gap-3 sm:grid-cols-2">
            {(["dev", "prod"] as const).map((p) => (
              <div
                key={p}
                className="space-y-1 rounded-lg border bg-card/40 p-3"
              >
                <div className="flex items-center gap-2">
                  <span className="font-mono text-sm font-medium">{p}</span>
                  {daemon?.profile === p && (
                    <Badge variant="outline" className="border-primary/40 text-[10px] text-primary">
                      active default
                    </Badge>
                  )}
                  <Badge variant="secondary" className="text-[10px]">
                    {PROFILES[p].unsatisfied} when a control cannot be satisfied
                  </Badge>
                </div>
                <p className="text-xs text-muted-foreground">{PROFILES[p].blurb}</p>
              </div>
            ))}
          </div>

          <Separator />

          <div className="space-y-2">
            <p className="flex items-center gap-1.5 text-[11px] font-medium tracking-wide text-muted-foreground uppercase">
              <Globe className="size-3.5" />
              Baseline egress ({BASELINE_EGRESS.length} domains)
            </p>
            <div className="flex flex-wrap gap-1">
              {BASELINE_EGRESS.map((d) => (
                <Badge key={d} variant="secondary" className="font-mono text-[10px]">
                  {d}
                </Badge>
              ))}
            </div>
            <p className="text-xs text-muted-foreground">
              The agent APIs, the common registries and the code hosts, so{" "}
              <code className="font-mono">npm install</code>,{" "}
              <code className="font-mono">pip install</code> and{" "}
              <code className="font-mono">git</code> work without enumerating them. Set{" "}
              <code className="font-mono">network.baseline: false</code> to drop them — it exists
              because <code className="font-mono">allow</code> could only ever <em>add</em>, leaving
              no way to decline <code className="font-mono">github.com</code>, which is a write
              endpoint and so an exfiltration channel for any token the agent holds.
            </p>
          </div>

          <Separator />

          <div className="space-y-1.5 text-xs text-muted-foreground">
            <p>
              <span className="font-medium text-foreground">
                A project <code className="font-mono">.sandbox.yaml</code> is untrusted input.
              </span>{" "}
              The privilege-relevant keys are refused from it —{" "}
              <code className="font-mono">
                image, workdir, user, home, runtime, mounts, secrets, env, env_allow, security.*,
                cache.paths
              </code>{" "}
              — along with any <code className="font-mono">network.mode</code> or{" "}
              <code className="font-mono">network.baseline</code> that weakens what is already in
              force. A project may tighten, never loosen.
            </p>
            <p>
              <span className="font-medium text-foreground">
                Discovery is bounded.
              </span>{" "}
              It stops at the repository root, else the home directory, else where it started — so a{" "}
              <code className="font-mono">.sandbox.yaml</code> in a shared parent like{" "}
              <code className="font-mono">/tmp</code> is not picked up.
            </p>
          </div>
        </CardContent>
      </Card>

      <RepositoriesCard />

      {/* The two snapshot cards are one section, and the anchor names it rather
          than either card: the Snapshots screen's settings button lands here,
          and somebody arriving from it is as likely to be after the bucket as
          the windows. scroll-mt clears the sticky header, without which the
          heading lands underneath it and the section looks like it starts at
          the second card. */}
      <section id="snapshots" className="scroll-mt-20 space-y-4">
        <SnapshotsCard />
        <SnapshotStorageCard />
      </section>

      {/* Below the repositories and above the daemon's own facts. Repositories
          are what people come here to change; the connection is set once, on the
          day a daemon moves to another machine. It stays ahead of the Daemon
          card because it decides *which* daemon those facts are about. */}
      <ConnectionCard />

      <Card className="surface-sheen gap-4">
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-sm font-medium">
            <PlugZap className="size-4 text-muted-foreground" />
            Daemon
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <dl className="grid gap-3 text-xs sm:grid-cols-2 lg:grid-cols-4">
            <Field label="Endpoint" value={apiBase()} mono />
            <Field
              label="Transport"
              value={mode === "live" ? "live daemon" : "fixtures"}
              tone={mode === "live" ? "text-contained" : "text-caution"}
            />
            <Field label="sandbox-cli" value={daemon?.version ?? "—"} mono />
            <Field
              label="Engine"
              value={daemon ? `${daemon.engine} ${daemon.engineVersion}` : "—"}
              mono
            />
          </dl>

          {daemon && (
            <dl className="grid gap-3 border-t pt-3 text-xs sm:grid-cols-2 lg:grid-cols-4">
              <Field label="Host" value={`${daemon.host.os}/${daemon.host.arch}`} mono />
              <Field label="CPUs" value={String(daemon.host.cpus)} />
              <Field
                label="Memory available"
                value={formatBytes(daemon.host.memBytes)}
                // On macOS the daemon's VM budget is the number that matters,
                // not the Mac's own RAM — so it is asked of the engine.
              />
              <Field label="Reported by" value="the container engine" />
            </dl>
          )}

          {mode !== "live" && (
            <div className="flex flex-wrap items-center gap-3 rounded-md border border-caution/30 bg-caution/5 p-3 text-xs">
              <Cpu className="size-4 shrink-0 text-caution" />
              <p className="min-w-0 flex-1 text-muted-foreground">
                Nothing answered on <code className="font-mono">{apiBase()}</code>, so every screen
                is showing fixtures. Nothing here reflects a real container.
              </p>
              <Button variant="outline" size="sm" className="h-7 text-xs" onClick={retry}>
                Retry
              </Button>
            </div>
          )}

          <div className="flex flex-wrap gap-2 border-t pt-3">
            <Button asChild variant="ghost" size="sm" className="h-7 text-xs">
              <a
                href="https://github.com/Amitgb14/sandbox-cli"
                target="_blank"
                rel="noreferrer"
              >
                <ExternalLink className="size-3.5" />
                Repository
              </a>
            </Button>
            <Button asChild variant="ghost" size="sm" className="h-7 text-xs">
              <a
                href="https://github.com/Amitgb14/sandbox-cli/blob/main/docs/AGENTS.md"
                target="_blank"
                rel="noreferrer"
              >
                <ExternalLink className="size-3.5" />
                Agent setup
              </a>
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

function Row({
  id,
  checked,
  onChange,
  label,
  hint,
}: {
  id: string;
  checked: boolean;
  onChange: (v: boolean) => void;
  label: string;
  hint: string;
}) {
  return (
    <div className="flex items-start gap-3">
      <Switch id={id} checked={checked} onCheckedChange={onChange} className="mt-0.5" />
      <div className="min-w-0 space-y-0.5">
        <Label htmlFor={id} className="text-xs font-medium">
          {label}
        </Label>
        <p className="text-xs leading-relaxed text-muted-foreground">{hint}</p>
      </div>
    </div>
  );
}

function Field({
  label,
  value,
  mono,
  tone,
}: {
  label: string;
  value: string;
  mono?: boolean;
  tone?: string;
}) {
  return (
    <div className="min-w-0 space-y-0.5">
      <dt className="truncate text-muted-foreground">{label}</dt>
      <dd className={`truncate ${mono ? "font-mono" : ""} ${tone ?? ""}`} title={value}>
        {value}
      </dd>
    </div>
  );
}
