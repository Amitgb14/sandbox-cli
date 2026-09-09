import type { Metadata } from "next";
import Link from "next/link";
import { ArrowLeft, ArrowUpRight, Check, ShieldCheck, X } from "lucide-react";
import { SiteHeader } from "@/components/site-header";
import { SiteFooter } from "@/components/site-footer";
import { Section, SectionHead } from "@/components/section-head";
import { CodeBlock } from "@/components/code-block";
import { buttonVariants } from "@/components/ui/button";
import { type NavEntry } from "@/lib/nav";
import {
  BASELINE,
  CAPTURED,
  CONVERSATION,
  FETCH,
  GUARANTEES,
  MIRROR,
  MODES,
  PATHS,
} from "@/lib/recover";
import { DOC_URL } from "@/lib/site";
import { cn } from "@/lib/utils";

const TITLE = "Snapshots and recovery — sandbox-cli";
const DESCRIPTION =
  "While a sandbox runs, the workspace is committed under refs/sandbox/ without touching your index, HEAD or branches. What each way of running actually captures, how to get files back, and why the conversation is recovered separately.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  openGraph: { title: TITLE, description: DESCRIPTION },
};

const NAV: NavEntry[] = [
  { kind: "link", href: "#paths", label: "What you actually get" },
  {
    kind: "group",
    label: "Getting work back",
    items: [
      {
        href: "#restore",
        label: "Restoring",
        hint: "three modes, and the one that overwrites",
      },
      {
        href: "#conversation",
        label: "The conversation",
        hint: "recovered separately, and usually the half that went missing",
      },
      {
        href: "#captured",
        label: "What is in a snapshot",
        hint: "untracked yes, gitignored no, and why",
      },
    ],
  },
  {
    kind: "group",
    label: "Off this machine",
    items: [
      {
        href: "#mirror",
        label: "Mirroring to S3",
        hint: "a git bundle, and a credential named rather than held",
      },
      {
        href: "#fetch",
        label: "Fetching it back",
        hint: "works on a machine that has never seen the repository",
      },
    ],
  },
  { kind: "link", href: "#guarantees", label: "What refuses" },
];

export default function RecoverPage() {
  return (
    <>
      <SiteHeader nav={NAV} homeHref="/" installHref="/#install" />

      <main id="top" className="flex flex-col">
        {/* ---------------------------------------------------------------- */}
        <Section className="pt-10 lg:pt-14">
          <Link
            href="/"
            className="mb-8 inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
          >
            <ArrowLeft className="size-3.5" />
            sandbox-cli
          </Link>

          <div className="flex max-w-3xl flex-col gap-4">
            <span className="eyebrow">
              <span className="h-px w-6 bg-current opacity-50" />
              snapshots &amp; recovery
            </span>
            <h1 className="text-[2.1rem] leading-[1.1] font-semibold tracking-[-0.03em] text-balance sm:text-[2.7rem]">
              Getting work back when a run dies badly
            </h1>
            <p className="text-[1.05rem] leading-relaxed text-pretty text-muted-foreground">
              Everything an agent does lands directly on your disk — that is
              what the bind mount is for — so when a run dies mid-write, the
              damage lands there too. While a sandbox is up, the workspace is
              committed into your repository&rsquo;s own object store under{" "}
              <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
                refs/sandbox/snapshots/
              </code>
              , written against a private index so your own index,{" "}
              <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
                HEAD
              </code>
              , branches and working tree are never touched.
            </p>
            <p className="text-[1.05rem] leading-relaxed text-pretty text-muted-foreground">
              <strong className="font-medium text-foreground">
                How much of that you get depends on how you started the agent
              </strong>
              , and the difference is invisible until you need it. That is the
              first section for a reason.
            </p>
            <div className="mt-2 flex flex-wrap items-center gap-2.5">
              <a href="#paths" className={cn(buttonVariants({ size: "sm" }))}>
                Start here
              </a>
              <a
                href={DOC_URL.guide}
                target="_blank"
                rel="noreferrer"
                className={cn(
                  buttonVariants({ size: "sm", variant: "outline" }),
                )}
              >
                The guide
                <ArrowUpRight className="size-3.5" />
              </a>
            </div>
          </div>
        </Section>

        {/* ---------------------------------------------------------------- */}
        <Section id="paths" tinted>
          <SectionHead
            eyebrow="what you actually get"
            title="Three ways to start an agent, three amounts of protection"
            lead={
              <>
                The safety net people picture — a copy every couple of minutes —
                exists on one of these rows. Knowing which one you are on is the
                difference between a recoverable afternoon and a restore that
                hands back the state you started from.
              </>
            }
          />

          <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
            {PATHS.map((p) => (
              <div
                key={p.how}
                className={cn(
                  "flex flex-col gap-3 rounded-xl border bg-card p-5",
                  p.level === "full" && "border-contained/40",
                  p.level === "none" && "border-exposed/40",
                  p.level === "one" && "border-caution/40",
                )}
              >
                <CodeBlock code={p.how} chrome={false} />
                <p
                  className={cn(
                    "text-[0.95rem] font-medium",
                    p.level === "full" && "text-contained",
                    p.level === "none" && "text-exposed",
                    p.level === "one" && "text-caution",
                  )}
                >
                  {p.cadence}
                </p>
                <p className="text-sm leading-relaxed text-muted-foreground">
                  {p.body}
                </p>
              </div>
            ))}
          </div>

          <div className="mt-8 rounded-xl border border-caution/40 bg-card p-5">
            <h3 className="mb-2 text-[1.05rem] font-medium">
              {BASELINE.title}
            </h3>
            <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
              {BASELINE.body}
            </p>
          </div>

          <p className="mt-6 max-w-3xl text-sm leading-relaxed text-muted-foreground">
            So if the work must survive an agent you are not watching, run it in
            the foreground — or take a snapshot yourself, from Studio or the
            SDK, at the moment that matters. A snapshot you asked for is
            recorded as one and is restorable; a baseline is not.
          </p>
        </Section>

        {/* ---------------------------------------------------------------- */}
        <Section id="restore">
          <SectionHead
            eyebrow="restoring"
            title="Look first, then pick a mode"
            lead={
              <>
                Run these from your normal checkout, even when the crash
                happened in a{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
                  --worktree
                </code>{" "}
                sandbox — worktrees share the repository the snapshots live in.
              </>
            }
          />

          <ol className="flex flex-col gap-0 divide-y rounded-xl border bg-card">
            {MODES.map((m) => (
              <li key={m.cmd} className="flex flex-col gap-2.5 p-5">
                <CodeBlock code={m.cmd} />
                <p className="text-sm leading-relaxed text-muted-foreground">
                  {m.what}
                </p>
                {m.careful ? (
                  <p className="text-sm leading-relaxed text-caution">
                    {m.careful}
                  </p>
                ) : null}
              </li>
            ))}
          </ol>

          <p className="mt-6 max-w-3xl text-sm leading-relaxed text-muted-foreground">
            Restoring never moves a branch you already have. A name that exists
            and points somewhere else is refused rather than overwritten, and
            one that already holds the snapshot reports that nothing needed
            doing.
          </p>
        </Section>

        {/* ---------------------------------------------------------------- */}
        <Section id="conversation" tinted>
          <SectionHead
            eyebrow="the conversation"
            title="The files are usually fine. The conversation is what goes"
            lead={
              <>
                After a crash the instinct is to hunt for missing files, and
                they are almost always already on disk. What actually disappears
                lives inside the container.
              </>
            }
          />

          <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
            {CONVERSATION.map((c) => (
              <div
                key={c.title}
                className="flex flex-col gap-2.5 rounded-xl border bg-card p-5"
              >
                <h3 className="text-[0.95rem] font-medium">{c.title}</h3>
                <p className="text-sm leading-relaxed text-muted-foreground">
                  {c.body}
                </p>
              </div>
            ))}
          </div>

          <div className="mt-8">
            <CodeBlock
              title="both halves"
              code={`sandbox-cli recover restore 20260908-235145      # the files, onto a branch
sandbox-cli claude context list                  # find the conversation
sandbox-cli claude --worktree sandbox-recover/… --resume <id>`}
            />
          </div>
        </Section>

        {/* ---------------------------------------------------------------- */}
        <Section id="captured">
          <SectionHead
            eyebrow="what is in a snapshot"
            title="A whole working tree, minus two things"
            lead={
              <>
                Each snapshot is a complete commit of the tree rather than a
                diff — git shares the unchanged blobs, so the cost is small and
                what you restore is a whole state.
              </>
            }
          />

          <div className="overflow-x-auto rounded-xl border bg-card">
            <table className="w-full text-sm">
              <tbody className="divide-y">
                {CAPTURED.map((c) => (
                  <tr key={c.what}>
                    <td className="w-10 p-4 align-top">
                      {c.included ? (
                        <Check
                          className="size-4 text-contained"
                          aria-label="captured"
                        />
                      ) : (
                        <X
                          className="size-4 text-exposed"
                          aria-label="not captured"
                        />
                      )}
                    </td>
                    <td className="p-4 pl-0 align-top font-medium">{c.what}</td>
                    <td className="p-4 pl-0 align-top leading-relaxed text-muted-foreground">
                      {c.body}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Section>

        {/* ---------------------------------------------------------------- */}
        <Section id="mirror" tinted>
          <SectionHead
            eyebrow="mirroring to s3"
            title="A copy that survives the machine"
            lead={
              <>
                Snapshots live in your own repository, which is fast and private
                and also the limit: a lost laptop loses them with everything
                else. Point{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
                  snapshot.s3
                </code>{" "}
                at a bucket — AWS, or MinIO, R2, Ceph and B2 through{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
                  endpoint
                </code>{" "}
                — and each one is uploaded as a git bundle.
              </>
            }
          />

          <CodeBlock
            code={MIRROR.yaml}
            lang="yaml"
            title="~/.config/sandbox/config.yaml"
          />

          <div className="mt-8 grid grid-cols-1 gap-4 md:grid-cols-2">
            {MIRROR.rules.map((r) => (
              <div
                key={r.title}
                className="flex flex-col gap-2.5 rounded-xl border bg-card p-5"
              >
                <h3 className="text-[0.95rem] font-medium">{r.title}</h3>
                <p className="text-sm leading-relaxed text-muted-foreground">
                  {r.body}
                </p>
              </div>
            ))}
          </div>
        </Section>

        {/* ---------------------------------------------------------------- */}
        <Section id="fetch">
          <SectionHead
            eyebrow="fetching it back"
            title="On a machine that has never seen the repository"
            lead={
              <>
                A small manifest is stored beside every bundle, which is what
                makes the bucket readable by a machine that has lost its rescue
                directory — or never had one.
              </>
            }
          />

          <ol className="flex flex-col gap-0 divide-y rounded-xl border bg-card">
            {FETCH.map((f) => (
              <li key={f.cmd} className="flex flex-col gap-2.5 p-5">
                <CodeBlock code={f.cmd} />
                <p className="text-sm leading-relaxed text-muted-foreground">
                  {f.what}
                </p>
              </li>
            ))}
          </ol>

          <p className="mt-6 max-w-3xl text-sm leading-relaxed text-muted-foreground">
            With nothing but git, the bundle still opens — which is the whole
            reason for the format:
          </p>
          <div className="mt-3">
            <CodeBlock
              code={`git init recovered && cd recovered
git fetch ../snap.bundle 'refs/sandbox/snapshots/*:refs/heads/snap/*'
git checkout snap/<id>`}
            />
          </div>
        </Section>

        {/* ---------------------------------------------------------------- */}
        <Section id="guarantees" tinted>
          <SectionHead
            eyebrow="what refuses"
            title="The rules that make this safe to leave on"
            lead={
              <>
                A safety net that could damage the thing it is protecting would
                not be worth running by default, so each of these is a refusal
                rather than a best effort.
              </>
            }
          />

          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            {GUARANTEES.map((g) => (
              <div
                key={g.title}
                className="flex flex-col gap-2.5 rounded-xl border bg-card p-5"
              >
                <div className="flex items-center gap-2">
                  <ShieldCheck className="size-4 shrink-0 text-contained" />
                  <h3 className="text-[0.95rem] font-medium">{g.title}</h3>
                </div>
                <p className="text-sm leading-relaxed text-muted-foreground">
                  {g.body}
                </p>
              </div>
            ))}
          </div>
        </Section>
      </main>

      <SiteFooter />
    </>
  );
}
