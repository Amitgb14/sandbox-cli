import { BoundaryMark } from "@/components/marks";

const COLS = [
  {
    title: "Page",
    links: [
      { href: "#threat", label: "Threat" },
      { href: "#boundary", label: "Boundary" },
      { href: "#features", label: "Features" },
      { href: "#compare", label: "Comparison" },
    ],
  },
  {
    title: "Docs",
    links: [
      { href: "https://github.com/Aegmis/sandbox-cli#readme", label: "README" },
      { href: "https://github.com/Aegmis/sandbox-cli/blob/main/docs/GUIDE.md", label: "User guide" },
      { href: "https://github.com/Aegmis/sandbox-cli/blob/main/docs/AGENTS.md", label: "Agent reference" },
      { href: "https://github.com/Aegmis/sandbox-cli/blob/main/docs/DEVELOPMENT.md", label: "Development" },
    ],
  },
  {
    title: "Project",
    links: [
      { href: "https://github.com/Aegmis/sandbox-cli", label: "GitHub" },
      { href: "https://github.com/Aegmis/sandbox-cli/issues", label: "Issues" },
      { href: "https://github.com/Aegmis/sandbox-cli/blob/main/LICENSE", label: "License" },
    ],
  },
];

export function SiteFooter() {
  return (
    <footer className="mt-20 border-t py-12">
      <div className="mx-auto w-full max-w-6xl px-6">
        <div className="flex flex-wrap justify-between gap-10">
          <div className="flex max-w-sm flex-col gap-3">
            <a href="#top" className="flex items-center gap-2 font-heading text-base font-bold tracking-tight">
              <span className="grid size-7 place-items-center rounded-md bg-contained text-background">
                <BoundaryMark className="size-4" />
              </span>
              sandbox-cli
            </a>
            <p className="text-sm text-muted-foreground">
              A wall between your coding agent and everything it was never meant to touch. Written in
              Go; standard library, cobra and yaml.v3 only.
            </p>
          </div>

          <div className="flex flex-wrap gap-14">
            {COLS.map((c) => (
              <div key={c.title} className="flex flex-col gap-1">
                <h5 className="mb-2 font-mono text-[0.68rem] uppercase tracking-[0.14em] text-muted-foreground">
                  {c.title}
                </h5>
                {c.links.map((l) => (
                  <a
                    key={l.label}
                    href={l.href}
                    {...(l.href.startsWith("http")
                      ? { target: "_blank", rel: "noopener noreferrer" }
                      : {})}
                    className="py-0.5 text-sm transition-colors hover:text-contained"
                  >
                    {l.label}
                  </a>
                ))}
              </div>
            ))}
          </div>
        </div>

        <div className="mt-11 flex flex-wrap justify-between gap-4 border-t pt-6 font-mono text-xs text-muted-foreground">
          <span>© {new Date().getFullYear()} sandbox-cli — MIT</span>
          <span>only declared mounts are host-connected</span>
        </div>
      </div>
    </footer>
  );
}
