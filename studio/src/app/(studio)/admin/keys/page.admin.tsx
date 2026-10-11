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
  useRevokeKey,
  useSetKeyExpiry,
  useUserSSHKeys,
  type CreatedInvite,
  type CreatedKey,
} from "@/lib/admin/api";
import { formatRelative } from "@/lib/format";

const SCOPES = ["sandbox:read", "sandbox:create", "sandbox:delete", "sandbox:ssh", "secrets:write", "org:create", "admin"];
const USER_SCOPES = ["sandbox:read", "sandbox:create", "sandbox:delete", "sandbox:ssh"];
const DAY = 86_400;

/**
 * What kind of user a key is for, as a starting point the form fills in:
 * a demo user has a policy and a key that lasts a week; a member, someone
 * using the service for good, has neither. Every field stays editable.
 */
const KINDS = {
  demo: { label: "Demo user", scopes: ["sandbox:read", "sandbox:create", "sandbox:delete"], expires: 7 * DAY },
  member: { label: "Member", scopes: USER_SCOPES, expires: 0 },
} as const;
type Kind = keyof typeof KINDS;

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
  const revoke = useRevokeKey();
  const assign = useAssignPolicy();
  const renew = useSetKeyExpiry();
  const reset = useResetPassword();
  const canEmail = useInvites().data?.canEmail ?? false;
  const [created, setCreated] = useState<CreatedKey | null>(null);
  const [invited, setInvited] = useState<CreatedInvite | null>(null);
  const rows = [...(data ?? [])].sort((a, b) => Number(!!a.revoked) - Number(!!b.revoked) || b.created.localeCompare(a.created));

  return (
    <div className="flex flex-col gap-8">
      <PageHeader title="Users & keys" description="Invite a person by email to sign in with a password, or issue a key for a program. Either acts as its user, in its tenant, with the scopes it was given; an expired or revoked one stops at once, and a user's last one takes their SSH access with it." />
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
        <SectionHeader title="Accounts and API keys" description="An account is a key whose user signs in with an email and password; its own secret is never shown." />
        {error ? <p className="text-sm text-destructive">{error.message}</p> : null}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Key</TableHead>
              <TableHead>User</TableHead>
              <TableHead>Scopes</TableHead>
              <TableHead>Policy</TableHead>
              <TableHead>Issued</TableHead>
              <TableHead>Expires</TableHead>
              <TableHead className="text-right" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((k) => (
              <TableRow key={k.id} className={k.revoked || expired(k.expires) ? "opacity-50" : ""}>
                <TableCell className="font-mono text-xs">{k.id}</TableCell>
                <TableCell className="text-sm">
                  {k.user}
                  {k.tenant ? <span className="text-muted-foreground">@{k.tenant}</span> : null}
                  {k.email ? (
                    <div className="text-xs text-muted-foreground" data-testid={`account-${k.user}`}>
                      {k.email}
                      {k.last_login ? ` · signed in ${formatRelative(k.last_login)}` : " · not signed in yet"}
                    </div>
                  ) : null}
                </TableCell>
                <TableCell>
                  <div className="flex flex-wrap gap-1">
                    {k.scopes.map((s) => (
                      <Badge key={s} variant="outline" className="font-mono text-[11px]">{s}</Badge>
                    ))}
                  </div>
                </TableCell>
                <TableCell>
                  {k.scopes.includes("admin") ? (
                    <span className="text-xs text-muted-foreground">admin: none</span>
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
                <TableCell className="text-xs text-muted-foreground">{formatRelative(k.created)}</TableCell>
                <TableCell className="text-xs text-muted-foreground" title={k.expires}>
                  {!k.expires ? "never" : expired(k.expires) ? <span className="text-caution">expired</span> : formatRelative(k.expires)}
                </TableCell>
                <TableCell className="text-right">
                  {k.revoked ? (
                    <span className="text-xs text-muted-foreground">revoked</span>
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
                      <Button size="sm" variant="ghost" disabled={revoke.isPending}
                        onClick={() => confirm(`Revoke ${k.id} (${k.user})? Every call with it is refused from now on.`) && revoke.mutate(k.id, { onError: (e) => toast.error(e.message), onSuccess: () => toast.success("Revoked") })}>
                        Revoke
                      </Button>
                    </>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </section>
      <UserSSHKeys />
    </div>
  );
}

function CreateKey({ onCreated, onInvited }: { onCreated: (k: CreatedKey) => void; onInvited: (iv: CreatedInvite) => void }) {
  const create = useCreateKey();
  const invite = useCreateInvite();
  const canEmail = useInvites().data?.canEmail ?? false;
  const [email, setEmail] = useState("");
  const [send, setSend] = useState(true);
  const { data: policies } = usePolicies();
  const [user, setUser] = useState("");
  const [tenant, setTenant] = useState("");
  const [kind, setKind] = useState<Kind>("member");
  const [scopes, setScopes] = useState<string[]>([...KINDS.member.scopes]);
  const [policy, setPolicy] = useState("");
  const [expires, setExpires] = useState(0);
  const choose = (k: Kind) => {
    setKind(k);
    setScopes([...KINDS[k].scopes]);
    setExpires(KINDS[k].expires);
    // A demo user's policy: "demo" where there is one, else the first.
    setPolicy(k === "demo" ? (policies?.find((p) => p.name === "demo") ?? policies?.[0])?.name ?? "" : "");
  };
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
            {(Object.keys(KINDS) as Kind[]).map((k) => (
              <Button key={k} type="button" size="sm" variant={kind === k ? "secondary" : "ghost"} aria-pressed={kind === k} onClick={() => choose(k)}>
                {KINDS[k].label}
              </Button>
            ))}
          </div>
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
          <div className="flex flex-wrap gap-x-5 gap-y-2">
            {SCOPES.map((s) => (
              <Label key={s} className="flex items-center gap-2 font-mono text-xs font-normal">
                <Checkbox checked={scopes.includes(s)} onCheckedChange={(on) => setScopes(on ? [...scopes, s] : scopes.filter((x) => x !== s))} />
                {s}
              </Label>
            ))}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button type="submit" value="invite" disabled={invite.isPending || !email.trim() || !user || scopes.length === 0}>
              <Mail aria-hidden /> Create invite
            </Button>
            <Button type="submit" value="key" variant="outline" disabled={create.isPending || !user || scopes.length === 0}>Issue key</Button>
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
