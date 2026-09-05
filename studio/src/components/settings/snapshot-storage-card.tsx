"use client";

import { useEffect, useState } from "react";
import { CheckCircle2, Cloud, KeyRound, Loader2, TriangleAlert } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import { Switch } from "@/components/ui/switch";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import {
  useCheckSnapshotStorage,
  useSetSnapshotSettings,
  useSnapshotSettings,
} from "@/lib/api/queries";
import type { SnapshotS3Settings, SnapshotUploadMode } from "@/lib/types";

/**
 * Where snapshots are kept besides this machine.
 *
 * The rule that shapes every field here: **this form holds no credential.** The
 * two key inputs take the *name* of an environment variable read on the daemon's
 * machine, never its contents — so the form, the request that saves it, the file
 * it is written to and this browser's memory are all places a secret has never
 * been. What the daemon reports back is a boolean and, when it is false, a
 * sentence naming the variable to export.
 *
 * The second thing worth knowing before reading the code: **retention above does
 * not reach the bucket.** Objects here are governed by the bucket's own
 * lifecycle rules, because deleting somebody's off-machine backup on a timer
 * that only runs when their laptop is open is a way to lose the copy that was
 * supposed to survive the laptop. The card says so rather than leaving it to be
 * discovered from a storage bill.
 */
export function SnapshotStorageCard() {
  const { data } = useSnapshotSettings();
  const save = useSetSnapshotSettings();
  const check = useCheckSnapshotStorage();

  const [form, setForm] = useState<SnapshotS3Settings | null>(null);

  // Seeded once the daemon answers, and only then. A controlled form starting
  // empty would save an empty bucket — which is how mirroring is turned *off* —
  // for anybody who pressed Save before the fetch landed.
  useEffect(() => {
    if (!data) return;
    setForm(
      data.s3 ?? {
        bucket: "",
        upload: "manual",
        credentialsResolved: false,
      },
    );
  }, [data]);

  const managed = !!data?.s3?.configManaged;
  const configured = !!data?.s3?.bucket;
  const set = (patch: Partial<SnapshotS3Settings>) =>
    setForm((f) => (f ? { ...f, ...patch } : f));

  return (
    <Card className="surface-sheen gap-4">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Cloud className="size-4" />
          Snapshot storage
          {configured && !managed && (
            <Badge variant="outline" className="text-[10px]">
              {data?.s3?.upload === "all" ? "every snapshot" : "checkpoints only"}
            </Badge>
          )}
          {managed && (
            <Badge variant="outline" className="text-[10px]">
              set in config.yaml
            </Badge>
          )}
        </CardTitle>
      </CardHeader>

      <CardContent className="space-y-4">
        <p className="text-xs leading-relaxed text-muted-foreground">
          A copy of each snapshot in an S3 bucket, as a git bundle — so a checkpoint
          survives the machine that took it. Works with AWS and anything S3-compatible
          (MinIO, R2, Ceph, B2). Leave the bucket empty to keep snapshots local.
        </p>

        {managed && (
          <p className="text-xs text-caution">
            This is configured in your config.yaml, which outranks this screen. Edit it
            there — a value saved here would be ignored at the next restart.
          </p>
        )}

        <div className="grid gap-3 sm:grid-cols-2">
          <Field
            id="s3-bucket"
            label="Bucket"
            placeholder="my-sandbox-snapshots"
            value={form?.bucket ?? ""}
            onChange={(v) => set({ bucket: v })}
            disabled={managed}
            help="Empty turns mirroring off."
          />
          <Field
            id="s3-region"
            label="Region"
            placeholder="us-east-1"
            value={form?.region ?? ""}
            onChange={(v) => set({ region: v })}
            disabled={managed}
            help="Defaults to us-east-1, which S3-compatible servers accept too."
          />
          <Field
            id="s3-endpoint"
            label="Endpoint"
            placeholder="https://minio.local:9000"
            value={form?.endpoint ?? ""}
            onChange={(v) => set({ endpoint: v })}
            disabled={managed}
            help="Empty addresses AWS."
          />
          <Field
            id="s3-prefix"
            label="Key prefix"
            placeholder="sandbox"
            value={form?.prefix ?? ""}
            onChange={(v) => set({ prefix: v })}
            disabled={managed}
            help="So one bucket can hold more than snapshots."
          />
        </div>

        <Row
          label="Path-style addressing"
          hint="Puts the bucket in the path rather than the hostname. Most self-hosted servers need this; AWS does not."
        >
          <Switch
            checked={!!form?.pathStyle}
            disabled={managed}
            onCheckedChange={(v) => set({ pathStyle: v })}
          />
        </Row>

        <Separator />

        <div className="space-y-1.5">
          <Label className="text-xs">What gets uploaded</Label>
          <ToggleGroup
            type="single"
            variant="outline"
            size="sm"
            value={form?.upload ?? "manual"}
            disabled={managed}
            onValueChange={(v) => v && set({ upload: v as SnapshotUploadMode })}
          >
            <ToggleGroupItem value="manual" className="px-3 text-xs">
              Checkpoints
            </ToggleGroupItem>
            <ToggleGroupItem value="all" className="px-3 text-xs">
              Everything
            </ToggleGroupItem>
            <ToggleGroupItem value="off" className="px-3 text-xs">
              Nothing
            </ToggleGroupItem>
          </ToggleGroup>
          <p className="text-[11px] leading-relaxed text-muted-foreground">
            {form?.upload === "all" ? (
              <>
                Crash snapshots too. Those are taken every two minutes for the length of
                every run, and each upload is sized like a clone — so this is a real cost
                per running agent, not a rounding error.
              </>
            ) : form?.upload === "off" ? (
              <>Configured but idle. Nothing leaves the machine until you change this.</>
            ) : (
              <>
                Snapshots you take on purpose, from this screen or the SDK. The crash-net
                timer stays local, which is what keeps this affordable.
              </>
            )}
          </p>
        </div>

        <Separator />

        <div className="space-y-2">
          <div className="flex items-center gap-2">
            <KeyRound className="size-3.5 text-muted-foreground" />
            <Label className="text-xs">Credentials</Label>
            {data?.s3 &&
              (data.s3.credentialsResolved ? (
                <Badge variant="outline" className="gap-1 text-[10px]">
                  <CheckCircle2 className="size-3" />
                  resolved
                </Badge>
              ) : (
                <Badge variant="outline" className="gap-1 text-[10px] text-caution">
                  <TriangleAlert className="size-3" />
                  not set
                </Badge>
              ))}
          </div>
          <p className="text-[11px] leading-relaxed text-muted-foreground">
            These are variable <em>names</em>, read from the environment the daemon was
            started in. The values never reach this page, this browser, or any file Studio
            writes — so there is nothing here to leak.
          </p>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field
              id="s3-key-env"
              label="Access key variable"
              placeholder="AWS_ACCESS_KEY_ID"
              mono
              value={form?.accessKeyEnv ?? ""}
              onChange={(v) => set({ accessKeyEnv: v })}
              disabled={managed}
            />
            <Field
              id="s3-secret-env"
              label="Secret key variable"
              placeholder="AWS_SECRET_ACCESS_KEY"
              mono
              value={form?.secretKeyEnv ?? ""}
              onChange={(v) => set({ secretKeyEnv: v })}
              disabled={managed}
            />
          </div>
          {data?.s3?.credentialsError && (
            <p className="text-[11px] text-caution">{data.s3.credentialsError}</p>
          )}
        </div>

        <Separator />

        <Row
          label="Retention does not reach the bucket"
          hint="The windows above prune the local copy. Objects here are governed by the bucket's own lifecycle rules — sandbox-cli never deletes them on a timer, because a backup that expires while your laptop is shut is not one."
        >
          <span />
        </Row>

        <div className="flex flex-wrap items-center gap-3">
          <Button
            size="sm"
            disabled={save.isPending || !data?.writable || managed || !form}
            onClick={() => form && save.mutate({ ...data!, s3: form })}
          >
            Save
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={check.isPending || !configured}
            onClick={() => check.mutate()}
          >
            {check.isPending && <Loader2 className="size-3.5 animate-spin" />}
            Test connection
          </Button>
          {!configured && (
            <p className="text-[11px] text-muted-foreground">
              Save a bucket first — the test asks the daemon about what it is configured
              with, not about what is typed here.
            </p>
          )}
        </div>

        {!data?.writable && (
          <p className="text-xs text-caution">
            No config directory could be resolved on the daemon&apos;s machine, so nothing
            can be saved here.
          </p>
        )}
      </CardContent>
    </Card>
  );
}

function Field({
  id,
  label,
  value,
  onChange,
  placeholder,
  help,
  disabled,
  mono,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  help?: string;
  disabled?: boolean;
  mono?: boolean;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id} className="text-xs">
        {label}
      </Label>
      <Input
        id={id}
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
        className={mono ? "font-mono text-xs" : ""}
      />
      {help && <p className="text-[11px] text-muted-foreground">{help}</p>}
    </div>
  );
}

function Row({
  label,
  hint,
  children,
}: {
  label: string;
  hint: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-start justify-between gap-4">
      <div className="min-w-0 space-y-0.5">
        <p className="text-xs font-medium">{label}</p>
        <p className="text-[11px] leading-relaxed text-muted-foreground">{hint}</p>
      </div>
      {children}
    </div>
  );
}
