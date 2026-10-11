"use client";

import { useState } from "react";
import { Check, Mail } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

type State = { kind: "editing" } | { kind: "sending" } | { kind: "done" } | { kind: "failed"; error: string };

/**
 * "Join the waitlist": a button that opens a form for a first name, a last name
 * and an email, posted to /api/waitlist (web/api/waitlist.ts), which mails it
 * to the waitlist inbox. The page is static; the function is the only part that
 * runs on a server, and on a host without one the form says it could not send.
 */
export function WaitlistButton({ className }: { className?: string }) {
  const [open, setOpen] = useState(false);
  const [state, setState] = useState<State>({ kind: "editing" });

  async function submit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    setState({ kind: "sending" });
    try {
      // With the slash: trailingSlash makes the host answer /api/waitlist with
      // a redirect, an extra round trip before the function runs.
      const res = await fetch("/api/waitlist/", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          firstName: form.get("firstName"),
          lastName: form.get("lastName"),
          email: form.get("email"),
          website: form.get("website"),
        }),
      });
      if (res.ok) {
        setState({ kind: "done" });
        return;
      }
      const body = await res.json().catch(() => ({}));
      setState({
        kind: "failed",
        error:
          typeof body.error === "string"
            ? body.error
            : `could not send the sign-up (${res.status}); please try again later`,
      });
    } catch {
      setState({ kind: "failed", error: "could not reach the server; check your connection and try again" });
    }
  }

  return (
    <>
      <Button
        size="lg"
        variant="outline"
        className={cn("gap-1.5 px-4", className)}
        onClick={() => {
          if (state.kind === "done" || state.kind === "failed") setState({ kind: "editing" });
          setOpen(true);
        }}
      >
        <Mail className="size-4" />
        Join the waitlist
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-md">
          {state.kind === "done" ? (
            <>
              <DialogHeader>
                <DialogTitle className="flex items-center gap-2">
                  <Check className="size-4 text-contained" /> You are on the waitlist
                </DialogTitle>
                <DialogDescription>
                  Thank you. We will write to you when there is news.
                </DialogDescription>
              </DialogHeader>
              <DialogFooter>
                <Button onClick={() => setOpen(false)}>Close</Button>
              </DialogFooter>
            </>
          ) : (
            <form onSubmit={submit} className="flex flex-col gap-4">
              <DialogHeader>
                <DialogTitle>Join the waitlist</DialogTitle>
                <DialogDescription>
                  Leave your name and email, and the sandbox-cli team will write to you when there is news. They
                  are sent to the team by email, and used for nothing else.
                </DialogDescription>
              </DialogHeader>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <Field label="First name" name="firstName" autoComplete="given-name" />
                <Field label="Last name" name="lastName" autoComplete="family-name" />
              </div>
              <Field label="Email" name="email" type="email" autoComplete="email" />
              {/* Left empty by people, filled in by simple bots; the server drops what has it. */}
              <input
                type="text"
                name="website"
                tabIndex={-1}
                autoComplete="off"
                aria-hidden="true"
                className="absolute -left-[9999px] h-px w-px opacity-0"
              />
              {state.kind === "failed" ? (
                <p role="alert" className="text-sm text-exposed">
                  {state.error.charAt(0).toUpperCase() + state.error.slice(1)}.
                </p>
              ) : null}
              <DialogFooter>
                <Button type="submit" disabled={state.kind === "sending"}>
                  {state.kind === "sending" ? "Sending…" : "Join"}
                </Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}

function Field({
  label,
  name,
  type = "text",
  autoComplete,
}: {
  label: string;
  name: string;
  type?: string;
  autoComplete: string;
}) {
  const id = `waitlist-${name}`;
  return (
    <label htmlFor={id} className="flex flex-col gap-1.5 text-sm font-medium">
      {label}
      <Input id={id} name={name} type={type} required maxLength={name === "email" ? 254 : 100} autoComplete={autoComplete} />
    </label>
  );
}
