"use client";

import { useState } from "react";
import { Mail, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { CopyButton } from "@/components/common/copy-button";
import { PageHeader, SectionHeader } from "@/components/common/page-header";
import { Gate } from "@/components/shell/gate";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
  useAdminOrgs,
  useAssignPolicy,
  useCreateInvite,
  useCreateKey,
  useDeletePolicy,
  useInvites,
  useKeys,
  usePolicies,
  usePutPolicy,
  useRemoveUserSSHKey,
  useResendInvite,
  useResetPassword,
  useRevokeInvite,
  useRestoreKey,
  useRevokeKey,
  useSetKeyExpiry,
  useUserSSHKeys,
  type CreatedInvite,
  useSuspendKey,
  type CreatedKey,
  type KeyInfo,
} from "@/lib/admin/api";
import { orgName } from "@/lib/org";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { formatRelative } from "@/lib/format";

/**
 * Every account is one kind: everything it may do, inside its own
 * organization — its sandboxes, SSH, secrets. Gateway-wide admin, this
 * screen among it, is the super admin's alone, and is given from the CLI
 * (sandbox-cli gateway keys create --scope admin), never from here.
 */
const ACCOUNT_SCOPES = ["sandbox:read", "sandbox:create", "sandbox:delete", "sandbox:ssh", "secrets:write", "org:create"];
const DAY = 86_400;


const EXPIRIES: { label: string; secs: number }[] = [
  { label: "never", secs: 0 },
  { label: "1 day", secs: DAY },
  { label: "7 days", secs: 7 * DAY },
  { label: "30 days", secs: 30 * DAY },
  { label: "90 days", secs: 90 * DAY },
];

/** Whether a key has stopped working by expiring. */
function expired(expires?: string): boolean {
  return !!expires && new Date(expires).getTime() <= Date.now();
}
/** Where a key stands, for the users filter. */
const STATUSES = ["active", "suspended", "expired", "revoked"] as const;
type Status = (typeof STATUSES)[number];
function statusOf(k: KeyInfo): Status {
  if (k.suspended) return "suspended";
  if (k.revoked) return "revoked";
  if (expired(k.expires)) return "expired";
  return "active";
}

/** The Select's value for no policy: Radix takes no empty value. */
const NO_POLICY = "-";

function lifetimeText(secs: number): string {
  if (!secs) return "no limit";
  if (secs % 3600 === 0) return `${secs / 3600} h`;
  if (secs % 60 === 0) return `${secs / 60} min`;
  return `${secs} s`;
}

/** A choice of policy, or none. */
function PolicySelect({ value, onChange, label, disabled }: { value: string; onChange: (v: string) => void; label: string; disabled?: boolean }) {
  const { data } = usePolicies();
  return (
    <Select value={value || NO_POLICY} onValueChange={(v) => onChange(v === NO_POLICY ? "" : v)} disabled={disabled}>
      <SelectTrigger aria-label={label} className="h-8 w-40 font-mono text-xs">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value={NO_POLICY}>no policy</SelectItem>
        {(data ?? []).map((p) => (
          <SelectItem key={p.name} value={p.name}>
            {p.name}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

/**
 * Users are their API keys: issue one, revoke one, and see or remove any
 * user's SSH keys. A new key's secret is in one answer only — the gateway
 * stores its hash — so it is shown once, here, and dropped when dismissed.
 */
function Keys() {
  const { data, error } = useKeys();
  const restore = useRestoreKey();
  const [revoking, setRevoking] = useState<KeyInfo | null>(null);
  const assign = useAssignPolicy();
  const renew = useSetKeyExpiry();
  const revokeNow = useRevokeKey();
  const reset = useResetPassword();
  const canEmail = useInvites().data?.canEmail ?? false;
  const [created, setCreated] = useState<CreatedKey | null>(null);
  const [invited, setInvited] = useState<CreatedInvite | null>(null);
  const { data: allOrgs } = useAdminOrgs();
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState<Status | "all">("all");
  const [role, setRole] = useState<"all" | "super" | "admin">("all");
  // A user's organizations: the one their key is in, then each they own.
  const orgsOf = (k: KeyInfo): string[] => {
    const own = orgName(k.tenant);
    const owned = (allOrgs ?? [])
      .filter((o) => o.name.toLowerCase() !== own.toLowerCase() && o.owned_by?.some((m) => m.user === k.user && (m.tenant ?? "") === (k.tenant ?? "")))
      .map((o) => o.name);
    return [own, ...owned];
  };
  // Live first, then suspended (still restorable), then revoked for good.
  const rank = (k: KeyInfo) => (k.suspended ? 1 : k.revoked ? 2 : 0);
  const q = query.trim().toLowerCase();
  const all = data ?? [];
  const rows = all
    .filter((k) => status === "all" || statusOf(k) === status)
    .filter((k) => role === "all" || (role === "super") === k.scopes.includes("admin"))
    .filter((k) => !q || [k.user, k.email ?? "", k.id, ...orgsOf(k), ...k.scopes, k.policy ?? ""].some((s) => s.toLowerCase().includes(q)))
    .sort((a, b) => rank(a) - rank(b) || b.created.localeCompare(a.created));

  return (
    <div className="flex flex-col gap-8">
      <PageHeader title="Users" description="Invite a person by email to sign in with a password, or issue a key for a program. Each user is the admin of their own organization and of nothing else; you are the super admin, of every one. Revoking suspends a user at once and is final an hour later." />
      {created ? (
        <Secret created={created} onDone={() => setCreated(null)} />
      ) : invited ? (
        <Invited invite={invited} onDone={() => setInvited(null)} />
      ) : (
        <CreateKey onCreated={setCreated} onInvited={setInvited} />
      )}
      <Invites onResent={(iv) => { setCreated(null); setInvited(iv); }} />
      <Policies />
      <section className="flex flex-col gap-3">
        <SectionHeader title="Users and their keys" description="An account is a key whose user signs in with an email and password; its own secret is never shown. A suspended one is refused everywhere and can be restored until the time it shows." />
        {error ? <p className="text-sm text-destructive">{error.message}</p> : null}
        <div className="flex flex-wrap items-center gap-2">
          <Input aria-label="Filter users" placeholder="Filter by user, email, organization, scope or policy" value={query} onChange={(e) => setQuery(e.target.value)} className="h-8 w-80 text-sm" />
          <Select value={role} onValueChange={(v) => setRole(v as typeof role)}>
            <SelectTrigger aria-label="Role" className="h-8 w-36 text-xs">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">every role</SelectItem>
              <SelectItem value="super">super admin</SelectItem>
              <SelectItem value="admin">admin</SelectItem>
            </SelectContent>
          </Select>
          <Select value={status} onValueChange={(v) => setStatus(v as typeof status)}>
            <SelectTrigger aria-label="Status" className="h-8 w-36 text-xs">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">any status</SelectItem>
              {STATUSES.map((s) => (
                <SelectItem key={s} value={s}>
                  {s}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <span className="text-xs text-muted-foreground" data-testid="users-count">
            {rows.length === all.length ? `${all.length} keys` : `${rows.length} of ${all.length} keys`}
          </span>
          {(q || status !== "all" || role !== "all") && (
            <Button size="sm" variant="ghost" onClick={() => { setQuery(""); setStatus("all"); setRole("all"); }}>
              Reset
            </Button>
          )}
        </div>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>User</TableHead>
              <TableHead>Organizations</TableHead>
              <TableHead>Scopes</TableHead>
              <TableHead>Policy</TableHead>
              <TableHead>Expires</TableHead>
              <TableHead className="text-right" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((k) => (
              <TableRow key={k.id} className={(k.revoked && !k.suspended) || expired(k.expires) ? "opacity-50" : ""}>
                <TableCell className="text-sm">
                  {k.user}
                  <div className="font-mono text-[11px] text-muted-foreground">{k.id}</div>
                  {k.personal ? (
                    <div className="text-xs text-muted-foreground" data-testid={`personal-${k.id}`}>
                      personal key · {k.name}
                    </div>
                  ) : null}
                  {k.email ? (
                    <div className="text-xs text-muted-foreground" data-testid={`account-${k.user}`}>
                      {k.email}
                      {k.last_login ? ` · signed in ${formatRelative(k.last_login)}` : " · not signed in yet"}
                    </div>
                  ) : null}
                </TableCell>
                <TableCell className="font-mono text-xs" data-testid={`org-${k.user}`}>
                  <div className="flex flex-col gap-0.5">
                    {orgsOf(k).map((o, i) => (
                      <span key={o} className={i ? "text-muted-foreground" : ""}>{o}</span>
                    ))}
                  </div>
                </TableCell>
                <TableCell>
                  <div className="flex flex-wrap gap-1">
                    {k.scopes.includes("admin") ? (
                      <Badge className="text-[11px]">super admin</Badge>
                    ) : (
                      k.scopes.map((s) => (
                        <Badge key={s} variant="outline" className="font-mono text-[11px]">{s}</Badge>
                      ))
                    )}
                  </div>
                </TableCell>
                <TableCell>
                  {k.scopes.includes("admin") ? (
                    <span className="text-xs text-muted-foreground">super admin: none</span>
                  ) : k.personal ? (
                    <span className="text-xs text-muted-foreground">{k.policy ?? "none"}, its user&apos;s</span>
                  ) : (
                    <PolicySelect
                      label={`Policy of ${k.user}`}
                      value={k.policy ?? ""}
                      disabled={k.revoked || assign.isPending}
                      onChange={(policy) =>
                        assign.mutate(
                          { user: k.user, tenant: k.tenant || undefined, policy },
                          { onError: (e) => toast.error(e.message), onSuccess: () => toast.success(policy ? `${k.user} now holds ${policy}` : `${k.user} holds no policy`) },
                        )
                      }
                    />
                  )}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground" title={k.expires}>
                  {!k.expires ? "never" : expired(k.expires) ? <span className="text-caution">expired</span> : formatRelative(k.expires)}
                </TableCell>
                <TableCell className="text-right">
                  {k.suspended ? (
                    <span className="inline-flex items-center gap-2">
                      <span className="text-xs text-caution" data-testid={`suspended-${k.user}`}>
                        suspended · revoked for good {k.restore_until ? new Date(k.restore_until).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : ""}
                      </span>
                      <Button size="sm" variant="outline" disabled={restore.isPending}
                        onClick={() => restore.mutate(k.id, { onError: (e) => toast.error(e.message), onSuccess: () => toast.success(`${k.user} restored`) })}>
                        Restore
                      </Button>
                    </span>
                  ) : k.revoked ? (
                    <span className="text-xs text-muted-foreground">revoked</span>
                  ) : k.personal ? (
                    // Revoked outright, not suspended: restoring one key alone
                    // could bring back one its user had meant to end.
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={revokeNow.isPending}
                      onClick={() =>
                        confirm(`Revoke ${k.user}'s personal key ${k.name}? Whatever uses it is refused at once; it cannot be restored.`) &&
                        revokeNow.mutate(k.id, { onError: (e) => toast.error(e.message), onSuccess: () => toast.success(`Revoked ${k.name}`) })
                      }
                    >
                      Revoke
                    </Button>
                  ) : (
                    <>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button size="sm" variant="ghost" aria-label={`Renew ${k.id}`} disabled={renew.isPending}>Renew</Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          {[
                            { label: "7 days from now", secs: 7 * DAY },
                            { label: "30 days from now", secs: 30 * DAY },
                            { label: "Never expires", secs: 0 },
                            { label: "Expire now", secs: -1 },
                          ].map((o) => (
                            <DropdownMenuItem
                              key={o.label}
                              onSelect={() =>
                                (o.secs >= 0 || confirm(`Expire ${k.id} (${k.user}) now? What it has open is ended.`)) &&
                                renew.mutate({ id: k.id, secs: o.secs }, { onError: (e) => toast.error(e.message), onSuccess: () => toast.success(`${k.user}: ${o.label.toLowerCase()}`) })
                              }
                            >
                              {o.label}
                            </DropdownMenuItem>
                          ))}
                          {k.email ? (
                            <DropdownMenuItem
                              onSelect={() =>
                                reset.mutate({ id: k.id, send: canEmail }, {
                                  onError: (e) => toast.error(e.message),
                                  onSuccess: (iv) => { setCreated(null); setInvited(iv); },
                                })
                              }
                            >
                              Reset password…
                            </DropdownMenuItem>
                          ) : null}
                        </DropdownMenuContent>
                      </DropdownMenu>
                      <Button size="sm" variant="ghost" onClick={() => setRevoking(k)}>
                        Revoke
                      </Button>
                    </>
                  )}
                </TableCell>
              </TableRow>
            ))}
            {rows.length === 0 && all.length > 0 && (
              <TableRow>
                <TableCell colSpan={6} className="py-6 text-center text-sm text-muted-foreground">No key matches.</TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </section>
      <UserSSHKeys />
      <RevokeDialog keyInfo={revoking} onClose={() => setRevoking(null)} />
    </div>
  );
}

/**
 * Revoking asks first, and is a suspension: the user is refused everywhere
 * at once, and for an hour the revoke can be taken back from the list.
 */
function RevokeDialog({ keyInfo: k, onClose }: { keyInfo: KeyInfo | null; onClose: () => void }) {
  const suspend = useSuspendKey();
  const at = new Date(Date.now() + 3_600_000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return (
    <Dialog open={!!k} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Revoke {k?.user}?</DialogTitle>
          <DialogDescription>
            {k?.email ? `${k.email}, of ${orgName(k.tenant)}. ` : k ? `Key ${k.id}, of ${orgName(k.tenant)}. ` : ""}
            Their access ends now: signing in, their keys and SSH are refused, their sign-ins end and their running jobs are cancelled. It is
            suspended until {at}, and you can restore it until then; after that it is revoked for good.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            disabled={suspend.isPending}
            onClick={() =>
              k &&
              suspend.mutate(k.id, {
                onError: (e) => toast.error(e.message),
                onSuccess: () => {
                  toast.success(`${k.user} suspended; restorable until ${at}`);
                  onClose();
                },
              })
            }
          >
            Revoke
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function CreateKey({ onCreated, onInvited }: { onCreated: (k: CreatedKey) => void; onInvited: (iv: CreatedInvite) => void }) {
  const create = useCreateKey();
  const invite = useCreateInvite();
  const canEmail = useInvites().data?.canEmail ?? false;
  const [email, setEmail] = useState("");
  const [send, setSend] = useState(true);
  const [user, setUser] = useState("");
  const [tenant, setTenant] = useState("");
  const scopes = ACCOUNT_SCOPES;
  const [policy, setPolicy] = useState("");
  const [expires, setExpires] = useState(0);
  return (
    <Card className="surface-sheen gap-0 py-0">
      <CardContent className="p-4">
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            // Enter in the email field invites; the key button issues a key.
            const submitter = (e.nativeEvent as SubmitEvent).submitter as HTMLButtonElement | null;
            if (submitter?.value !== "key" && email.trim()) {
              invite.mutate(
                {
                  email: email.trim(), user, tenant: tenant || undefined, scopes, policy: policy || undefined,
                  expires_in_secs: expires || undefined, send_email: (canEmail && send) || undefined,
                },
                {
                  onSuccess: (iv) => {
                    onInvited(iv);
                    setEmail("");
                    setUser("");
                    setTenant("");
                  },
                  onError: (err) => toast.error(err.message),
                },
              );
              return;
            }
            create.mutate(
              { user, tenant: tenant || undefined, scopes, policy: policy || undefined, expires_in_secs: expires || undefined },
              {
                onSuccess: (k) => {
                  onCreated(k);
                  setUser("");
                  setTenant("");
                },
                onError: (err) => toast.error(err.message),
              },
            );
          }}
        >
          <p className="text-sm font-medium">Add a user</p>
          <div className="flex flex-wrap gap-2">
            <Input
              aria-label="Email"
              type="email"
              placeholder="email (to invite a person)"
              value={email}
              onChange={(e) => {
                const prev = userFromEmail(email);
                setEmail(e.target.value);
                // The user name follows the address until it is typed over.
                if (!user || user === prev) setUser(userFromEmail(e.target.value));
              }}
              className="w-64"
            />
            <Input aria-label="User" placeholder="user" value={user} onChange={(e) => setUser(e.target.value)} className="w-48 font-mono" required />
            <Input aria-label="Tenant" placeholder={email.trim() ? "tenant (default: its own)" : "tenant (optional)"} value={tenant} onChange={(e) => setTenant(e.target.value)} className="w-48 font-mono" />
            {scopes.includes("admin") ? null : <PolicySelect label="Policy" value={policy} onChange={setPolicy} />}
            <Select value={String(expires)} onValueChange={(v) => setExpires(Number(v))}>
              <SelectTrigger aria-label="Key expires" className="h-8 w-36 text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {EXPIRIES.map((e) => (
                  <SelectItem key={e.secs} value={String(e.secs)}>
                    {e.secs ? `expires in ${e.label}` : "never expires"}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <p className="text-xs text-muted-foreground" data-testid="account-access">
            Every user is the admin of their own organization: sandboxes, SSH and secrets there, and nothing in anyone else&apos;s. A policy and an
            expiry, if you pick them, are all that differ.
          </p>
          <div className="flex flex-wrap items-center gap-2">
            <Button type="submit" value="invite" disabled={invite.isPending || !email.trim() || !user}>
              <Mail aria-hidden /> Create invite
            </Button>
            <Button type="submit" value="key" variant="outline" disabled={create.isPending || !user}>Issue key</Button>
            {canEmail ? (
              <Label className="flex items-center gap-2 text-xs font-normal">
                <Checkbox checked={send} onCheckedChange={(on) => setSend(on === true)} aria-label="Email the invite" />
                email the invite from the gateway
              </Label>
            ) : null}
            <span className="text-xs text-muted-foreground">
              An invite is a link the person opens once to choose a password; a key is for a program, or a person you hand it to.
            </span>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

/**
 * The policies users' sandboxes are held to, on top of the nodes' own limits.
 * A change holds for sandboxes created after it; a running one keeps its end,
 * which is moved on its own page.
 */
function Policies() {
  const { data, error } = usePolicies();
  const put = usePutPolicy();
  const del = useDeletePolicy();
  const [name, setName] = useState("");
  const [minutes, setMinutes] = useState("30");
  return (
    <section className="flex flex-col gap-3">
      <SectionHeader
        title="Policies"
        description="How long each sandbox of a user holding the policy may exist, busy or not. A change holds for sandboxes created after it; admin keys hold none."
      />
      {error ? <p className="text-sm text-destructive">{error.message}</p> : null}
      {data?.length ? (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Policy</TableHead>
              <TableHead>Max lifetime</TableHead>
              <TableHead className="text-right" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.map((p) => (
              <TableRow key={p.name}>
                <TableCell className="font-mono text-xs">{p.name}</TableCell>
                <TableCell className="text-sm">{lifetimeText(p.max_lifetime_secs)}</TableCell>
                <TableCell className="text-right">
                  <Button size="sm" variant="ghost" onClick={() => { setName(p.name); setMinutes(String(p.max_lifetime_secs / 60)); }}>
                    Edit
                  </Button>
                  <Button size="sm" variant="ghost" disabled={del.isPending}
                    onClick={() => confirm(`Remove policy ${p.name}?`) && del.mutate(p.name, { onError: (e) => toast.error(e.message), onSuccess: () => toast.success("Removed") })}>
                    Remove
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      ) : null}
      <form
        className="flex flex-wrap items-center gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          const mins = Number(minutes);
          if (!Number.isFinite(mins) || mins < 0) return toast.error("Minutes: a number, 0 or more");
          put.mutate(
            { name: name.trim(), max_lifetime_secs: Math.round(mins * 60) },
            { onError: (err) => toast.error(err.message), onSuccess: (p) => { toast.success(`Saved policy ${p.name}`); setName(""); } },
          );
        }}
      >
        <Input aria-label="Policy name" placeholder="policy, e.g. demo" value={name} onChange={(e) => setName(e.target.value)} className="w-48 font-mono" required />
        <Input aria-label="Max lifetime in minutes" type="number" min={0} step={1} value={minutes} onChange={(e) => setMinutes(e.target.value)} className="w-28" />
        <span className="text-sm text-muted-foreground">minutes (0: no limit)</span>
        <Button type="submit" variant="outline" disabled={put.isPending || !name.trim()}>Save policy</Button>
      </form>
    </section>
  );
}

/** A user name from an address's local part: what the gateway accepts of it. */
function userFromEmail(email: string): string {
  return email.split("@")[0].replace(/[^A-Za-z0-9._+-]/g, "").replace(/^[^A-Za-z0-9]+/, "").slice(0, 64);
}

/**
 * An invite just made: its link, once — the gateway keeps only the token's
 * hash — with a button that opens the admin's own mail program with the
 * message written. Nothing is sent from here.
 */
function Invited({ invite, onDone }: { invite: CreatedInvite; onDone: () => void }) {
  const hosted = process.env.NEXT_PUBLIC_STUDIO_HOSTED === "on";
  const origin = hosted ? window.location.origin : "https://YOUR-STUDIO";
  const link = `${origin}/#invite=${invite.token}`;
  const until = new Date(invite.expires).toLocaleString();
  const subject = invite.reset ? "Set a new password for Sandbox Studio" : "Your invite to Sandbox Studio";
  const body = [
    "Hi,",
    "",
    invite.reset
      ? "Open this link to set a new password for Sandbox Studio:"
      : "You're invited to Sandbox Studio. Open this link to choose a password and sign in:",
    "",
    link,
    "",
    `The link works once, until ${until}.`,
    `After that, sign in at ${origin} with ${invite.email} and your password.`,
  ].join("\n");
  const mailto = `mailto:${encodeURIComponent(invite.email)}?subject=${encodeURIComponent(subject)}&body=${encodeURIComponent(body)}`;
  return (
    <div className="flex flex-col gap-3 rounded-lg border border-caution/40 bg-caution/10 p-4">
      <p className="flex items-center gap-2 text-sm font-medium">
        <TriangleAlert className="size-4" aria-hidden />
        {invite.reset ? `Password reset for ${invite.email}` : `Invite for ${invite.email}`}: send the link now, it is shown only this once.
      </p>
      {invite.emailed ? (
        <p className="text-sm" data-testid="invite-emailed">Emailed to {invite.email}.</p>
      ) : invite.email_error ? (
        <p className="text-sm text-destructive" role="alert">Not emailed: {invite.email_error}. Send the link yourself.</p>
      ) : null}
      <p className="text-xs text-muted-foreground">
        It works once, until {until}.{" "}
        {invite.reset ? "The old password works until it is used." : `Opening it, ${invite.user} chooses a password and is signed in.`}
        {hosted ? "" : " Put your hosted Studio's address in place of https://YOUR-STUDIO."}
        {invite.scopes.includes("admin") ? " An admin signs in only while the hosted Studio runs with --allow-admin." : ""}
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <code data-testid="new-invite-link" className="min-w-0 flex-1 truncate rounded bg-background px-2 py-1 font-mono text-xs">{link}</code>
        {/* The literal in a hosted build only (scripts/check-hosted.mjs). */}
        <CopyButton value={link} label={process.env.NEXT_PUBLIC_STUDIO_HOSTED === "on" ? "Copy invite link" : "Copy link"} size="sm" />
        <Button size="sm" variant="outline" asChild>
          <a href={mailto} data-testid="email-invite">
            <Mail aria-hidden /> Email invite
          </a>
        </Button>
        <Button size="sm" variant="ghost" onClick={onDone}>Done</Button>
      </div>
    </div>
  );
}

/**
 * Invites not yet used: who they are for, until when, and a way to send one
 * again or withdraw it. Resending makes a new link (the gateway keeps only
 * the old one's hash, so it cannot be sent twice), and the old link stops.
 */
function Invites({ onResent }: { onResent: (iv: CreatedInvite) => void }) {
  const { data: list, error } = useInvites();
  const data = list?.invites;
  const canEmail = list?.canEmail ?? false;
  const revoke = useRevokeInvite();
  const resend = useResendInvite();
  if (!error && !data?.length) return null;
  return (
    <section className="flex flex-col gap-3">
      <SectionHeader
        title="Pending invites"
        description={`Links sent and not yet used. Resend makes a new link, good for two days${canEmail ? ", and emails it" : ""}; the one sent before stops working.`}
      />
      {error ? <p className="text-sm text-destructive">{error.message}</p> : null}
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Email</TableHead>
            <TableHead>User</TableHead>
            <TableHead>For</TableHead>
            <TableHead>Link works until</TableHead>
            <TableHead className="text-right" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {(data ?? []).map((iv) => (
            <TableRow key={iv.id}>
              <TableCell className="text-sm">{iv.email}</TableCell>
              <TableCell className="text-sm">
                {iv.user}
                {iv.tenant ? <span className="text-muted-foreground">@{iv.tenant}</span> : null}
              </TableCell>
              <TableCell className="text-xs text-muted-foreground">
                {iv.reset ? "a new password" : [iv.policy ? `policy ${iv.policy}` : "no policy", iv.expires_in_secs ? `account lasts ${Math.round(iv.expires_in_secs / DAY)} d` : "account for good"].join(", ")}
              </TableCell>
              <TableCell className="text-xs text-muted-foreground" title={iv.expires}>{formatRelative(iv.expires)}</TableCell>
              <TableCell className="text-right">
                <Button size="sm" variant="ghost" disabled={resend.isPending} aria-label={`Resend the invite for ${iv.email}`}
                  onClick={() =>
                    resend.mutate(
                      { id: iv.id, send: canEmail },
                      { onError: (e) => toast.error(e.message), onSuccess: (r) => { onResent(r); window.scrollTo({ top: 0, behavior: "smooth" }); } },
                    )
                  }>
                  Resend
                </Button>
                <Button size="sm" variant="ghost" disabled={revoke.isPending}
                  onClick={() => confirm(`Withdraw the invite for ${iv.email}?`) && revoke.mutate(iv.id, { onError: (e) => toast.error(e.message), onSuccess: () => toast.success("Withdrawn") })}>
                  Withdraw
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </section>
  );
}

/** The new key's secret, once: dropped on Done, and kept in no cache (useCreateKey). */
function Secret({ created, onDone }: { created: CreatedKey; onDone: () => void }) {
  return (
    <div className="flex flex-col gap-3 rounded-lg border border-caution/40 bg-caution/10 p-4">
      <p className="flex items-center gap-2 text-sm font-medium">
        <TriangleAlert className="size-4" aria-hidden /> Copy the secret for {created.user} now: it is shown only this once.
      </p>
      <p className="text-xs text-muted-foreground">
        The gateway keeps only its hash. Hand it over as you would a password, in a file: sandbox-cli context add NAME URL --token-file FILE.
      </p>
      <div className="flex items-center gap-2">
        <code data-testid="new-key-secret" className="min-w-0 flex-1 truncate rounded bg-background px-2 py-1 font-mono text-xs">{created.secret}</code>
        <CopyButton value={created.secret} label="Copy secret" size="sm" />
        <Button size="sm" variant="ghost" onClick={onDone}>Done</Button>
      </div>
      {process.env.NEXT_PUBLIC_STUDIO_HOSTED === "on" ? <InviteLink created={created} /> : null}
    </div>
  );
}

/**
 * The link that signs the new user in to this Studio: its address and the
 * key, in the fragment, which a browser never sends to a server. It is the
 * key, so it is shown once, with the secret.
 */
function InviteLink({ created }: { created: CreatedKey }) {
  const link = `${window.location.origin}/#key=${created.secret}`;
  return (
    <div className="flex flex-col gap-1.5">
      <p className="text-xs text-muted-foreground">
        Or send them their invite link: opening it signs them in here.
        {created.scopes.includes("admin") ? " An admin's link signs in only while this Studio runs with --allow-admin." : ""}
      </p>
      <div className="flex items-center gap-2">
        <code data-testid="new-key-invite" className="min-w-0 flex-1 truncate rounded bg-background px-2 py-1 font-mono text-xs">{link}</code>
        <CopyButton value={link} label="Copy invite link" size="sm" />
      </div>
    </div>
  );
}

function UserSSHKeys() {
  const [input, setInput] = useState("");
  const [user, setUser] = useState("");
  const { data, error } = useUserSSHKeys(user);
  const remove = useRemoveUserSSHKey();
  return (
    <section className="flex flex-col gap-3">
      <SectionHeader title="A user's SSH keys" description="Any user's registered public keys; removing one ends its logins." />
      <form className="flex gap-2" onSubmit={(e) => { e.preventDefault(); setUser(input.trim()); }}>
        <Input aria-label="SSH keys of user" placeholder="user" value={input} onChange={(e) => setInput(e.target.value)} className="w-48 font-mono" required />
        <Button type="submit" variant="outline">Show</Button>
      </form>
      {error ? <p className="text-sm text-destructive">{error.message}</p> : null}
      {user && data && (
        data.length === 0 ? (
          <p className="text-sm text-muted-foreground">{user} has no SSH keys.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Fingerprint</TableHead>
                <TableHead>Sandbox</TableHead>
                <TableHead>Added</TableHead>
                <TableHead className="text-right" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.map((k) => (
                <TableRow key={k.id}>
                  <TableCell className="font-mono text-xs" title={k.key}>{k.fingerprint}</TableCell>
                  <TableCell className="font-mono text-xs">{k.sandbox || "any of theirs"}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">{formatRelative(k.created)}</TableCell>
                  <TableCell className="text-right">
                    <Button size="sm" variant="ghost" disabled={remove.isPending}
                      onClick={() => confirm(`Remove ${k.fingerprint} from ${user}?`) && remove.mutate(k.id, { onError: (e) => toast.error(e.message) })}>
                      Remove
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )
      )}
    </section>
  );
}

export default function KeysPage() {
  return (
    <Gate need="admin">
      <Keys />
    </Gate>
  );
}
