"use client";

import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { KeyRound, LogOut } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { SidebarMenu, SidebarMenuButton, SidebarMenuItem } from "@/components/ui/sidebar";
import {
  apiFetch,
  inviteInfo,
  logIn,
  register,
  SESSION_ENDED,
  signIn,
  signOut,
  takeInviteKey,
  takeInviteToken,
} from "@/lib/api/client";

/** The gateway's shortest password. */
const PASSWORD_MIN = 10;

/**
 * Set while this browser has had a session, so a page loaded after it ended
 * (signed out elsewhere, expired, revoked) says so rather than only asking
 * to sign in. Not a credential: the session is the HttpOnly cookie.
 */
const HAD_SESSION = "sandbox-studio-had-session";

function remember(had: boolean) {
  try {
    if (had) localStorage.setItem(HAD_SESSION, "1");
    else localStorage.removeItem(HAD_SESSION);
  } catch {
    // A browser that refuses storage only loses the "has ended" wording.
  }
}

function hadSession(): boolean {
  try {
    return localStorage.getItem(HAD_SESSION) === "1";
  } catch {
    return false;
  }
}

type State =
  | "checking"
  | "ready"
  | { signIn: true; ended: boolean }
  | { error: string }
  | { invite: string; email: string; reset?: boolean };

/**
 * A hosted Studio's sign-in, in front of everything else: nothing of
 * Studio is shown, and nothing asks the server for data, until there is a
 * session. An invite link carries the user's gateway key in the fragment,
 * or a sign-up link an invite token for which a password is chosen first;
 * without either, the page asks whether there is a session and, if not,
 * shows the sign-in page. A session that ends while Studio is open brings
 * the sign-in page back.
 *
 * In a local build this renders its children and nothing else, and the rest
 * is removed by the minifier (the comparison is with the literal).
 */
export function HostedSignIn({ children }: { children: React.ReactNode }) {
  // "checking" until the effect has looked at the address: the static
  // export is rendered without one, and must match on first paint.
  const [state, setState] = useState<State>("checking");
  useEffect(() => {
    if (process.env.NEXT_PUBLIC_STUDIO_HOSTED !== "on") return;
    const check = (first: boolean) => {
      // A sign-up link: the page asks for a password before anything else.
      const invite = takeInviteToken();
      if (invite === "") {
        setState({ error: "This link does not carry an invite. Copy the whole link, including everything after #." });
        return;
      }
      if (invite !== null) {
        setState("checking");
        inviteInfo(invite)
          .then((iv) => setState({ invite, email: iv.email, reset: iv.reset }))
          .catch((e: Error) => setState({ error: e.message }));
        return;
      }
      const key = takeInviteKey();
      if (key === null) {
        if (!first) return;
        apiFetch("/session", { noOrg: true })
          .then(() => {
            remember(true);
            setState("ready");
          })
          .catch(() => setState({ signIn: true, ended: hadSession() }));
        return;
      }
      if (key === "") {
        setState({ error: "This link does not carry a key. Copy the whole invite link, including everything after #." });
        return;
      }
      setState("checking");
      signIn(key)
        .then(signedIn)
        .catch((e: Error) => setState({ error: e.message }));
    };
    check(true);
    // An invite link opened in a tab already on Studio changes only the
    // fragment, which does not load the page again.
    const onHash = () => check(false);
    const onEnded = () => setState({ signIn: true, ended: hadSession() });
    window.addEventListener("hashchange", onHash);
    window.addEventListener(SESSION_ENDED, onEnded);
    return () => {
      window.removeEventListener("hashchange", onHash);
      window.removeEventListener(SESSION_ENDED, onEnded);
    };
  }, []);

  if (process.env.NEXT_PUBLIC_STUDIO_HOSTED !== "on") return <>{children}</>;
  if (state === "checking") return null;
  if (state === "ready") return <>{children}</>;
  if ("invite" in state) {
    return (
      <AuthPage>
        <SetPassword invite={state.invite} email={state.email} reset={state.reset} />
      </AuthPage>
    );
  }
  if ("signIn" in state) {
    return (
      <AuthPage>
        <SignInPage ended={state.ended} />
      </AuthPage>
    );
  }
  return (
    <AuthPage>
      <div className="flex flex-col gap-4">
        <SessionNotice title="This invite link did not sign you in" detail={state.error} />
        <Button variant="outline" onClick={() => window.location.replace("/")}>
          Go to sign in
        </Button>
      </div>
    </AuthPage>
  );
}

/** After a sign-in: a new session, perhaps another user's, so nothing of the last stays. */
function signedIn() {
  remember(true);
  window.location.replace("/");
}

/** The page every sign-in screen is on: the name, and one card in the middle. */
function AuthPage({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-svh flex-col items-center justify-center gap-6 bg-muted/30 p-6">
      <div className="text-lg font-semibold tracking-tight">sandbox·studio</div>
      <div className="w-full max-w-sm rounded-xl border bg-background p-6 shadow-sm">{children}</div>
    </div>
  );
}

/** Email and password, or for whoever holds one, an API key. */
function SignInPage({ ended }: { ended: boolean }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [key, setKey] = useState("");
  const [withKey, setWithKey] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const fail = (err: Error) => {
    setError(err.message);
    setBusy(false);
  };
  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h1 className="text-xl font-semibold tracking-tight">{ended ? "Your session has ended" : "Sign in"}</h1>
        <p className="text-sm text-muted-foreground">
          {ended ? "Sign in again to continue." : "Use the email and password you chose with your invite."}
        </p>
      </div>
      <form
        className="flex flex-col gap-4"
        aria-label="Sign in"
        onSubmit={(e) => {
          e.preventDefault();
          setBusy(true);
          setError("");
          remember(false);
          (withKey ? signIn(key.trim()) : logIn(email, password)).then(signedIn).catch(fail);
        }}
      >
        {withKey ? (
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="login-key">API key</Label>
            <Input id="login-key" type="password" autoComplete="off" placeholder="sgk_…" required value={key} onChange={(e) => setKey(e.target.value)} className="font-mono" />
          </div>
        ) : (
          <>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="login-email">Email</Label>
              <Input id="login-email" type="email" autoComplete="username" required autoFocus value={email} onChange={(e) => setEmail(e.target.value)} />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="login-password">Password</Label>
              <Input id="login-password" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
            </div>
          </>
        )}
        {error && <p className="text-sm text-destructive" role="alert">{error}</p>}
        <Button type="submit" disabled={busy} className="w-full">
          Sign in
        </Button>
      </form>
      <div className="flex flex-col gap-2 text-center text-xs text-muted-foreground">
        <button type="button" className="underline-offset-4 hover:underline" onClick={() => { setWithKey(!withKey); setError(""); }}>
          {withKey ? "Sign in with email and password" : "Sign in with an API key instead"}
        </button>
        <p>No account? Ask whoever runs this Studio for an invite. Forgot your password? They can send you a new link.</p>
      </div>
    </div>
  );
}

function SessionNotice({ title, detail }: { title: string; detail: string }) {
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-caution/40 bg-caution/10 p-4">
      <div className="flex items-center gap-2 text-sm font-medium">
        <KeyRound className="size-4" aria-hidden /> {title}
      </div>
      <p className="text-sm text-muted-foreground">{detail}</p>
    </div>
  );
}

/** An invite's page: the address it was sent to, and a password to choose. */
function SetPassword({ invite, email, reset }: { invite: string; email: string; reset?: boolean }) {
  const [password, setPassword] = useState("");
  const [again, setAgain] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const short = password.length > 0 && password.length < PASSWORD_MIN;
  const differ = again.length > 0 && again !== password;
  return (
    <form
      className="flex flex-col gap-4"
      aria-label={reset ? "Set a new password" : "Create your account"}
      onSubmit={(e) => {
        e.preventDefault();
        setBusy(true);
        setError("");
        register(invite, password)
          .then(signedIn)
          .catch((err: Error) => {
            setError(err.message);
            setBusy(false);
          });
      }}
    >
      <div className="flex items-center gap-2 text-base font-semibold">
        <KeyRound className="size-4" aria-hidden /> {reset ? "Set a new password" : "Create your account"}
      </div>
      <p className="text-sm text-muted-foreground">
        For <span className="font-medium text-foreground">{email}</span>. You sign in with this address and the password you
        choose here; the link works only once.
      </p>
      {/* For the browser's password manager, which saves the pair. */}
      <input type="email" autoComplete="username" value={email} readOnly hidden />
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="new-password">Password</Label>
        <Input id="new-password" type="password" autoComplete="new-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
        {short && <p className="text-xs text-muted-foreground">At least {PASSWORD_MIN} characters.</p>}
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="new-password-again">Password again</Label>
        <Input id="new-password-again" type="password" autoComplete="new-password" required value={again} onChange={(e) => setAgain(e.target.value)} />
        {differ && <p className="text-xs text-muted-foreground">The two are not the same.</p>}
      </div>
      {error && <p className="text-sm text-destructive" role="alert">{error}</p>}
      <div>
        <Button type="submit" className="w-full" disabled={busy || password.length < PASSWORD_MIN || again !== password}>
          {reset ? "Set password" : "Create account"}
        </Button>
      </div>
    </form>
  );
}

/** Ends this browser's session; the page then says so. Hosted builds only. */
export function SignOutButton() {
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  if (process.env.NEXT_PUBLIC_STUDIO_HOSTED !== "on") return null;
  return (
    <SidebarMenu>
      <SidebarMenuItem>
    <SidebarMenuButton
      disabled={busy}
      tooltip="Sign out"
      onClick={() => {
        setBusy(true);
        signOut()
          .then(() => remember(false))
          .catch((e: Error) => toast.error(e.message))
          .finally(() => {
            setBusy(false);
            // Nothing of this session is kept for the next one.
            qc.clear();
            window.dispatchEvent(new Event(SESSION_ENDED));
          });
      }}
    >
      <LogOut aria-hidden />
      <span>Sign out</span>
    </SidebarMenuButton>
      </SidebarMenuItem>
    </SidebarMenu>
  );
}
