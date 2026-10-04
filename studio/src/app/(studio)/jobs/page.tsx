"use client";

import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { ListChecks, Plus } from "lucide-react";
import { toast } from "sonner";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { StatusBadge } from "@/components/common/status-badge";
import { Gate } from "@/components/shell/gate";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { useCancelJob, useJobs, useSubmitJob } from "@/lib/api/gateway";
import { useAgents } from "@/lib/api/queries";
import { useCan } from "@/lib/caller";
import { formatArgv, formatRelative, splitArgs } from "@/lib/format";
import type { Job, JobSpec } from "@/lib/types";

const list = (s: string) => s.split(",").map((x) => x.trim()).filter(Boolean);

/** What a job runs, in a few words: its command, or its agent. */
function jobWhat(j: Job): string {
  if (j.spec.agent) return `${j.spec.agent}${j.spec.prompts?.length ? ` · ${j.spec.prompts.length} prompts` : ""}`;
  return formatArgv(j.spec.command ?? []);
}

/**
 * Jobs: work the gateway runs to completion after the request has gone, each
 * run in a sandbox of its own, with what was asked to be kept — output and
 * files — kept after the sandbox is gone.
 */
function Jobs() {
  const can = useCan();
  const { data, isLoading, error } = useJobs();
  const cancel = useCancelJob();
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const rows = data ?? [];

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Jobs"
        description="Commands and agent runs the gateway runs to completion, each in a sandbox of its own. Output and kept files stay until the job expires."
        actions={
          can("sandbox:create") && !open ? (
            <Button size="sm" onClick={() => setOpen(true)}>
              <Plus className="size-4" />
              Submit job
            </Button>
          ) : undefined
        }
      />
      {open && can("sandbox:create") && <SubmitJob onClose={() => setOpen(false)} />}
      {error ? (
        <p className="text-sm text-destructive">{error.message}</p>
      ) : !isLoading && rows.length === 0 ? (
        <EmptyState icon={ListChecks} title="No jobs" description="Submit one here, or with sandbox-cli job run." />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Job</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Runs</TableHead>
              <TableHead>What</TableHead>
              <TableHead>Created</TableHead>
              <TableHead className="text-right" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((j) => (
              <TableRow key={j.id} className="cursor-pointer" onClick={(e) => !(e.target as HTMLElement).closest("a,button") && router.push(`/job?id=${j.id}`)}>
                <TableCell>
                  <Link href={`/job?id=${j.id}`} className="flex flex-col hover:underline">
                    <span className="text-sm font-medium">{j.name || j.id}</span>
                    {j.name ? <span className="font-mono text-[11px] text-muted-foreground">{j.id}</span> : null}
                  </Link>
                </TableCell>
                <TableCell>
                  <StatusBadge outcome={j.state} size="sm" />
                </TableCell>
                <TableCell className="font-mono text-xs text-muted-foreground tabular-nums">
                  {j.succeeded} ok · {j.failed} failed · {j.running} running · {j.queued} queued
                </TableCell>
                <TableCell className="max-w-64 truncate font-mono text-xs text-muted-foreground">{jobWhat(j)}</TableCell>
                <TableCell className="text-xs text-muted-foreground">{formatRelative(j.created_at)}</TableCell>
                <TableCell className="text-right">
                  {j.state === "running" && can("sandbox:create") && (
                    <Button size="sm" variant="ghost" disabled={cancel.isPending}
                      onClick={() => confirm(`Cancel ${j.name || j.id}? Its running sandboxes are terminated.`) && cancel.mutate(j.id, { onError: (e) => toast.error(e.message), onSuccess: () => toast.success("Cancelled") })}>
                      Cancel
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}

function SubmitJob({ onClose }: { onClose: () => void }) {
  const router = useRouter();
  const { data: agents } = useAgents();
  const submit = useSubmitJob();
  const [kind, setKind] = useState<"command" | "agent">("command");
  const [name, setName] = useState("");
  const [image, setImage] = useState("");
  const [command, setCommand] = useState("");
  const [agent, setAgent] = useState("claude");
  const [prompt, setPrompt] = useState("");
  const [retries, setRetries] = useState("");
  const [timeout, setTimeoutSecs] = useState("");
  const [secrets, setSecrets] = useState("");
  const [files, setFiles] = useState("");

  function send(e: React.FormEvent) {
    e.preventDefault();
    const spec: JobSpec = { name: name || undefined, image: image || undefined };
    if (kind === "command") spec.command = splitArgs(command);
    else {
      spec.agent = agent;
      spec.prompt = prompt;
    }
    if (retries) spec.retries = Number(retries);
    if (timeout) spec.timeout_secs = Number(timeout);
    if (list(secrets).length) spec.secrets = list(secrets);
    if (list(files).length) spec.keep = { files: list(files) };
    submit.mutate(spec, {
      onSuccess: (j) => {
        toast.success(`Submitted ${j.id}`);
        router.push(`/job?id=${j.id}`);
      },
      onError: (err) => toast.error(err.message),
    });
  }

  return (
    <Card className="surface-sheen gap-0 py-0">
      <CardContent className="p-4">
        <form onSubmit={send} className="grid gap-4 md:grid-cols-2">
          <div className="flex flex-col gap-1.5">
            <Label>Runs</Label>
            <Select value={kind} onValueChange={(v) => setKind(v as "command" | "agent")}>
              <SelectTrigger aria-label="Runs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="command">A command</SelectItem>
                <SelectItem value="agent">An agent, unattended</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="job-name">Name</Label>
            <Input id="job-name" placeholder="optional" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          {kind === "command" ? (
            <div className="flex flex-col gap-1.5 md:col-span-2">
              <Label htmlFor="job-command">Command</Label>
              <Input id="job-command" placeholder="make test" value={command} onChange={(e) => setCommand(e.target.value)} className="font-mono" required />
            </div>
          ) : (
            <>
              <div className="flex flex-col gap-1.5">
                <Label>Agent</Label>
                <Select value={agent} onValueChange={setAgent}>
                  <SelectTrigger aria-label="Agent">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {(agents ?? []).map((a) => (
                      <SelectItem key={a.name} value={a.name}>
                        {a.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="flex flex-col gap-1.5 md:col-span-2">
                <Label htmlFor="job-prompt">Prompt</Label>
                <Textarea id="job-prompt" value={prompt} onChange={(e) => setPrompt(e.target.value)} required />
              </div>
            </>
          )}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="job-image">Image</Label>
            <Input id="job-image" placeholder="the gateway's default" value={image} onChange={(e) => setImage(e.target.value)} className="font-mono" />
          </div>
          <div className="grid grid-cols-2 gap-2">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="job-retries">Retries</Label>
              <Input id="job-retries" placeholder="0" value={retries} onChange={(e) => setRetries(e.target.value.replace(/\D/g, ""))} />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="job-timeout">Timeout, seconds</Label>
              <Input id="job-timeout" placeholder="3600" value={timeout} onChange={(e) => setTimeoutSecs(e.target.value.replace(/\D/g, ""))} />
            </div>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="job-secrets">Secrets</Label>
            <Input id="job-secrets" placeholder="NAME, OTHER (set in the environment)" value={secrets} onChange={(e) => setSecrets(e.target.value)} className="font-mono" />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="job-files">Keep files</Label>
            <Input id="job-files" placeholder="/sandbox/home/report.md, …" value={files} onChange={(e) => setFiles(e.target.value)} className="font-mono" />
          </div>
          <div className="flex gap-2 md:col-span-2">
            <Button type="submit" disabled={submit.isPending}>Submit</Button>
            <Button type="button" variant="ghost" onClick={onClose}>Close</Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

export default function JobsPage() {
  return (
    <Gate need="gateway">
      <Jobs />
    </Gate>
  );
}
