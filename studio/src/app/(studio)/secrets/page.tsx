"use client";

import { useState } from "react";
import { LockKeyhole } from "lucide-react";
import { toast } from "sonner";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { Gate } from "@/components/shell/gate";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useRemoveSecret, useSecrets, useSetSecret } from "@/lib/api/gateway";
import { useCan } from "@/lib/caller";
import { formatRelative } from "@/lib/format";

/**
 * The tenant's secrets, by name. A value goes in through PUT and is never
 * returned by any call, so there is nothing here that could show one: the
 * field is a password field, cleared once the gateway has it.
 */
function Secrets() {
  const can = useCan();
  const writable = can("secrets:write");
  const { data, isLoading, error } = useSecrets();
  const set = useSetSecret();
  const remove = useRemoveSecret();
  const [name, setName] = useState("");
  const [value, setValue] = useState("");

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Secrets"
        description="Name one in a job or a service (secrets: [NAME]) and it is set in each sandbox's environment. Any key of the tenant may name them; values are write-only and never shown."
      />
      {writable && (
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            set.mutate(
              { name, value },
              {
                onSuccess: () => {
                  toast.success(`Set ${name}`);
                  setName("");
                  setValue("");
                },
                onError: (err) => toast.error(err.message),
              },
            );
          }}
        >
          <Input aria-label="Secret name" placeholder="NAME" value={name} onChange={(e) => setName(e.target.value)} className="w-56 font-mono" required />
          <Input aria-label="Secret value" type="password" autoComplete="off" placeholder="value" value={value} onChange={(e) => setValue(e.target.value)} className="w-72 font-mono" required />
          <Button type="submit" disabled={set.isPending}>Set secret</Button>
        </form>
      )}
      {error ? (
        <p className="text-sm text-destructive">{error.message}</p>
      ) : !isLoading && (data ?? []).length === 0 ? (
        <EmptyState icon={LockKeyhole} title="No secrets" description={writable ? "Set one above." : "This key cannot set them (secrets:write)."} />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Updated</TableHead>
              <TableHead className="text-right" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {(data ?? []).map((s) => (
              <TableRow key={s.name}>
                <TableCell className="font-mono text-sm">{s.name}</TableCell>
                <TableCell className="text-xs text-muted-foreground">{formatRelative(s.updated_at)}</TableCell>
                <TableCell className="text-right">
                  {writable && (
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={remove.isPending}
                      onClick={() =>
                        confirm(`Remove ${s.name}? Jobs and services naming it are refused new sandboxes.`) &&
                        remove.mutate(s.name, { onError: (e) => toast.error(e.message), onSuccess: () => toast.success("Removed") })
                      }
                    >
                      Remove
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

export default function SecretsPage() {
  return (
    <Gate need="gateway">
      <Secrets />
    </Gate>
  );
}
