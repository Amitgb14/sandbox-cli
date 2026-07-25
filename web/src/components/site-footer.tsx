import { GithubMark, Wordmark } from "@/components/logo";
import { DOC_URL, REPO_URL, VERSION } from "@/lib/site";

const COLUMNS = [
  {
    title: "Docs",
    links: [
      { label: "User guide", href: DOC_URL.guide },
      { label: "Agent reference", href: DOC_URL.agents },
      { label: "Security model", href: DOC_URL.security },
      { label: "Changelog", href: DOC_URL.changelog },
    ],
  },
  {
    title: "Project",
    links: [
      { label: "Source", href: REPO_URL },
      { label: "Releases", href: `${REPO_URL}/releases` },
      { label: "Issues", href: `${REPO_URL}/issues` },
      { label: "Development", href: DOC_URL.development },
    ],
  },
  {
    title: "On this page",
    links: [
      { label: "Why", href: "#threat" },
      { label: "Features", href: "#features" },
      { label: "Config file", href: "#config" },
      { label: "Agents", href: "#agents" },
      { label: "Compare", href: "#compare" },
    ],
  },
];

export function SiteFooter() {
  return (
    <footer className="border-t bg-surface">
      <div className="mx-auto grid w-full max-w-6xl grid-cols-1 gap-10 px-5 py-12 sm:grid-cols-2 sm:px-6 lg:grid-cols-[1.4fr_repeat(3,minmax(0,1fr))]">
        <div className="flex flex-col gap-3">
          <Wordmark className="text-[1.05rem]" />
          <p className="max-w-xs text-sm leading-relaxed text-muted-foreground">
            Run AI coding agents at full autonomy inside a disposable container, with the blast
            radius limited to the project they were already meant to edit.
          </p>
          <a
            href={REPO_URL}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex w-fit items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
          >
            <GithubMark className="size-3.5" />
            Amitgb14/sandbox-cli
          </a>
        </div>

        {COLUMNS.map((c) => (
          <div key={c.title} className="flex flex-col gap-2.5">
            <p className="eyebrow">{c.title}</p>
            <ul className="flex flex-col gap-1.5">
              {c.links.map((l) => (
                <li key={l.label}>
                  <a
                    href={l.href}
                    target={l.href.startsWith("#") ? undefined : "_blank"}
                    rel={l.href.startsWith("#") ? undefined : "noopener noreferrer"}
                    className="text-sm text-muted-foreground transition-colors hover:text-foreground"
                  >
                    {l.label}
                  </a>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </div>

      <div className="border-t">
        <div className="mx-auto flex w-full max-w-6xl flex-wrap items-center justify-between gap-3 px-5 py-5 text-xs text-muted-foreground sm:px-6">
          <p>
            MIT licensed · written in Go · v{VERSION}. Not affiliated with Anthropic, OpenAI, Google
            or any other agent vendor.
          </p>
          <p className="font-mono">isolation is a boundary, not a guarantee — read the model</p>
        </div>
      </div>
    </footer>
  );
}
