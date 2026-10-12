"use client";

import { useState } from "react";
import { KeyRound, LockKeyhole } from "lucide-react";
import { toast } from "sonner";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { Gate } from "@/components/shell/gate";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useRegistryLogins, useRemoveRegistryLogin, useRemoveSecret, useSecrets, useSetRegistryLogin, useSetSecret } from "@/lib/api/gateway";
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
        description="Name one in a job or a service and it is set in each sandbox's environment: VAR=name sets it as VAR (OPENAI_API_KEY=openai-prod), a bare NAME under its own name. Any key of the tenant may name them; values are write-only and never shown."
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
          <Input aria-label="Secret name" placeholder="name (openai-prod, API_TOKEN)" value={name} onChange={(e) => setName(e.target.value)} className="w-56 font-mono" required />
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

/**
 * The organization's registry logins: a user name and a token per registry,
 * with which the gateway pulls the organization's private images, and no one
 * else's. The token is write-only, as a secret's value is.
 */
function RegistryLogins() {
  const can = useCan();
  const writable = can("secrets:write");
  const { data, isLoading, error } = useRegistryLogins();
  const set = useSetRegistryLogin();
  const remove = useRemoveRegistryLogin();
  const [registry, setRegistry] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");

  return (
    <section className="flex flex-col gap-3">
      <div>
        <h2 className="text-sm font-semibold">Registry logins</h2>
        <p className="text-sm text-muted-foreground">
          For private images: every sandbox, job and service of this organization whose image is on the registry is pulled with its login, and no other
          organization&apos;s is. A token is write-only; on ghcr.io it needs read:packages.
        </p>
      </div>
      {writable && (
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            set.mutate(
              { registry: registry.trim(), username: username.trim(), password },
              {
                onSuccess: () => {
                  toast.success(`Images on ${registry.trim()} are pulled as ${username.trim()}`);
                  setRegistry("");
                  setUsername("");
                  setPassword("");
                },
                onError: (err) => toast.error(err.message),
              },
            );
          }}
        >
          <Input aria-label="Registry" placeholder="registry (ghcr.io)" value={registry} onChange={(e) => setRegistry(e.target.value)} className="w-48 font-mono" required />
          <Input aria-label="Registry username" placeholder="username" autoComplete="off" value={username} onChange={(e) => setUsername(e.target.value)} className="w-44 font-mono" required />
          <Input aria-label="Registry token" type="password" autoComplete="off" placeholder="token" value={password} onChange={(e) => setPassword(e.target.value)} className="w-64 font-mono" required />
          <Button type="submit" disabled={set.isPending}>Save login</Button>
        </form>
      )}
      {error ? (
        <p className="text-sm text-destructive">{error.message}</p>
      ) : !isLoading && (data ?? []).length === 0 ? (
        <EmptyState icon={KeyRound} title="No registry logins" description={writable ? "Images are pulled anonymously: public ones only." : "This key cannot set them (secrets:write)."} />
      ) : (
        <Table data-testid="registry-logins">
          <TableHeader>
            <TableRow>
              <TableHead>Registry</TableHead>
              <TableHead>Username</TableHead>
              <TableHead>Updated</TableHead>
              <TableHead className="text-right" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {(data ?? []).map((l) => (
              <TableRow key={l.registry}>
                <TableCell className="font-mono text-sm">{l.registry}</TableCell>
                <TableCell className="font-mono text-sm">{l.username}</TableCell>
                <TableCell className="text-xs text-muted-foreground">{formatRelative(l.updated_at)}</TableCell>
                <TableCell className="text-right">
                  {writable && (
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={remove.isPending}
                      onClick={() =>
                        confirm(`Remove the login for ${l.registry}? Its private images can no longer be pulled.`) &&
                        remove.mutate(l.registry, { onError: (e) => toast.error(e.message), onSuccess: () => toast.success("Removed") })
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
    </section>
  );
}

export default function SecretsPage() {
  return (
    <Gate need="gateway">
      <div className="flex flex-col gap-10">
        <Secrets />
        <RegistryLogins />
      </div>
    </Gate>
  );
}
