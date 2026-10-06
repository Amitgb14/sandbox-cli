/**
 * The documentation manifest: which of the repository's Markdown files the
 * site renders under /docs, at which address, in which segment of the sidebar.
 *
 * The repository's docs/ is the single source. Nothing here copies a word of
 * it: every page is read from its file at build time (src/lib/markdown.ts) and
 * rendered into the static export, so a change to a doc is a change to the
 * site the next time it is built — and CI rebuilds the site whenever docs/
 * changes (.github/workflows/ci.yml, the `web` filter).
 *
 * A page's address is its slug, not its file name, so a file can move in the
 * repository without breaking a link anyone saved; links between docs are
 * rewritten through this table (`pageBySource`), and a relative link to a file
 * that is not here goes to GitHub instead.
 */

import { BLOB, EDIT, docPath } from "@/lib/site";

export type DocPage = {
  /** The address under /docs; "" is /docs itself. */
  slug: string;
  /** The Markdown file, relative to the repository root. */
  source: string;
  /** The sidebar's label: short, and the same noun the rest of the site uses. */
  title: string;
  /** One line for search engines and link previews. */
  description: string;
  /** Set for a page generated from code: where it comes from, instead of "Edit". */
  generatedFrom?: { label: string; href: string };
};

export type DocSegment = { title: string; pages: DocPage[] };

export const DOC_SEGMENTS: DocSegment[] = [
  {
    title: "Getting started",
    pages: [
      {
        slug: "",
        source: "docs/README.md",
        title: "Overview",
        description: "Every page of sandbox-cli's documentation: getting started, the components, a fleet behind a gateway, and the reference.",
      },
      {
        slug: "local-macos",
        source: "docs/local-macos.md",
        title: "Local on a Mac",
        description: "Running sandboxd on a Mac with the native container runtime.",
      },
      {
        slug: "self-hosting",
        source: "docs/self-hosting.md",
        title: "Self-hosting on Linux",
        description: "sandboxd on a Linux machine with KVM: install, TLS and tokens, the network default, pools, volumes, the audit log and egress.",
      },
    ],
  },
  {
    title: "Components",
    pages: [
      {
        slug: "cli",
        source: "docs/cli.md",
        title: "sandbox-cli",
        description: "Every sandbox-cli command and flag, generated from the CLI itself.",
        generatedFrom: { label: "Generated from the command tree", href: `${BLOB}/internal/cli/docs.go` },
      },
      {
        slug: "sandboxd",
        source: "docs/sandboxd.md",
        title: "sandboxd",
        description: "The sandboxd reference: flags, the policy file, node mode and metrics.",
      },
      {
        slug: "studio",
        source: "docs/studio.md",
        title: "Studio",
        description: "Studio, the browser view of your sandboxes: its screens, gateway scopes, organizations and the hosted build.",
      },
      {
        slug: "desktop",
        source: "docs/desktop.md",
        title: "Desktop",
        description: "A sandbox with a screen, a terminal and a browser, used from Studio's Desktop tab.",
      },
      {
        slug: "sdk",
        source: "sdk/README.md",
        title: "SDKs",
        description: "The Python and TypeScript clients of the Sandbox API.",
      },
    ],
  },
  {
    title: "Fleet",
    pages: [
      {
        slug: "fleet",
        source: "docs/fleet.md",
        title: "Gateway",
        description: "sandbox-gateway in front of one machine or many: certificates, nodes, users' keys, the security model and scheduling.",
      },
      {
        slug: "ssh",
        source: "docs/ssh.md",
        title: "SSH",
        description: "One SSH port for every sandbox in a fleet: keys, short-lived tokens, scp, sftp and port forwards.",
      },
      {
        slug: "organizations",
        source: "docs/organizations.md",
        title: "Organizations",
        description: "Tenants users create and share on a gateway, chosen per request with X-Sandbox-Org.",
      },
      {
        slug: "jobs",
        source: "docs/jobs.md",
        title: "Jobs & secrets",
        description: "Commands and agent runs a gateway runs after you have gone, batches, the secret store and notify webhooks.",
      },
      {
        slug: "services",
        source: "docs/services.md",
        title: "Services & router",
        description: "Services a gateway keeps running: health checks, rolling updates, placement and the HTTP router.",
      },
      {
        slug: "operations",
        source: "docs/operations.md",
        title: "Operations",
        description: "Operating a fleet: metrics, the audit log, drain, lost nodes, revocation and the admin API.",
      },
    ],
  },
  {
    title: "Reference",
    pages: [
      {
        slug: "api",
        source: "docs/api/v1.md",
        title: "API v1",
        description: "Sandbox API v1, served the same way on a Mac, a Linux machine and a gateway.",
      },
      {
        slug: "security",
        source: "docs/security/README.md",
        title: "Security",
        description: "The boundary, what crosses it and how it is checked, the profiles, and who can reach the API.",
      },
      {
        slug: "security/secrets",
        source: "docs/security/secrets.md",
        title: "Secrets",
        description: "What sandbox-cli protects about a secret, what it does not, and how to make a leak cheap.",
      },
    ],
  },
];

/** Every page, in sidebar order: the order previous and next follow. */
export const DOC_PAGES: DocPage[] = DOC_SEGMENTS.flatMap((s) => s.pages);

export function pageBySlug(slug: string): DocPage | undefined {
  return DOC_PAGES.find((p) => p.slug === slug);
}

export function pageBySource(source: string): DocPage | undefined {
  return DOC_PAGES.find((p) => p.source === source);
}

export function segmentOf(page: DocPage): DocSegment | undefined {
  return DOC_SEGMENTS.find((s) => s.pages.includes(page));
}

export function neighbours(page: DocPage): { prev?: DocPage; next?: DocPage } {
  const i = DOC_PAGES.indexOf(page);
  return { prev: DOC_PAGES[i - 1], next: DOC_PAGES[i + 1] };
}

export function pageHref(page: DocPage, anchor?: string) {
  return docPath(page.slug, anchor);
}

/** Where the "Edit on GitHub" link goes. */
export function editHref(page: DocPage) {
  return `${EDIT}/${page.source}`;
}
