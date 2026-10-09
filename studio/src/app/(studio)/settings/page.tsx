"use client";

import { PageHeader, SectionHeader } from "@/components/common/page-header";
import { EgressRules } from "@/components/settings/egress-rules";
import { Badge } from "@/components/ui/badge";
import { useInfo } from "@/lib/api/queries";

/**
 * The context Studio talks to, what that sandboxd can deliver, and the egress
 * rules Studio adds to every launch. Switching
 * context is `sandbox-cli studio
 * --context …`: a Studio is one context's, which is what keeps its token one
 * sandboxd's.
 */
export default function SettingsPage() {
  const { data: info } = useInfo();
  const caps = info?.capabilities;

  return (
    <div className="flex flex-col gap-8">
      <PageHeader title="Settings" />

      <section className="flex flex-col gap-3">
        <SectionHeader title="Context" description="Which sandboxd this Studio talks to. Start Studio with --context to use another." />
        {info ? (
          <dl className="grid max-w-xl grid-cols-[10rem_1fr] gap-y-1.5 text-sm">
            <dt className="text-muted-foreground">context</dt><dd className="font-mono">{info.context}</dd>
            <dt className="text-muted-foreground">sandbox-cli</dt><dd className="font-mono">{info.version}</dd>
            <dt className="text-muted-foreground">backend</dt><dd className="font-mono">{caps?.backend ?? info.error}</dd>
            <dt className="text-muted-foreground">API</dt><dd className="font-mono">{caps?.api_version}</dd>
            <dt className="text-muted-foreground">network</dt>
            <dd className="font-mono">default {caps?.network.default.mode} · ceiling {caps?.network.ceiling}</dd>
            <dt className="text-muted-foreground">limits</dt>
            <dd className="font-mono">{caps ? `${caps.limits.max_cpus} cpus · ${caps.limits.max_memory_mb} MiB · ${caps.limits.max_disk_mb} MiB disk` : ""}</dd>
          </dl>
        ) : null}
        {caps && (
          <div className="flex flex-wrap gap-1.5">
            {Object.entries(caps.capabilities).sort().map(([k, v]) => (
              <Badge key={k} variant="outline" className={v ? "border-contained/40 text-contained" : "text-muted-foreground line-through"}>{k}</Badge>
            ))}
          </div>
        )}
        {caps?.network.default.allow?.length ? (
          <p className="max-w-3xl text-xs text-muted-foreground">
            Default allowlist: <span className="font-mono">{caps.network.default.allow.join(", ")}</span>
          </p>
        ) : null}
      </section>

      <EgressRules defaultMode={caps?.network.default.mode} ceiling={caps?.network.ceiling} />
    </div>
  );
}
