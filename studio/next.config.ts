import type { NextConfig } from "next";

/**
 * Studio is a static export, embedded in sandbox-cli and served by
 * `sandbox-cli studio` from the same origin as its API — so there is no
 * server-side rendering and no API base URL to configure. `make studio`
 * builds it into internal/studio/ui.
 *
 * Trailing slashes make every route a directory with an index.html, which is
 * what a plain file server (Go's, here) serves without rewrites.
 *
 * One thing is read from the environment, at build time only:
 * NEXT_PUBLIC_STUDIO_ADMIN=off builds Studio without the gateway's admin
 * screens, for a dashboard hosted for many tenants. The admin pages are named
 * page.admin.tsx, which is a page only when "admin.tsx" is a page extension,
 * so in such a build they are not routes and nothing they import — lib/admin
 * — is bundled. `npm run check:admin-off` builds one and checks.
 */
const admin = process.env.NEXT_PUBLIC_STUDIO_ADMIN !== "off";

const nextConfig: NextConfig = {
  output: "export",
  trailingSlash: true,
  images: { unoptimized: true },
  pageExtensions: admin ? ["admin.tsx", "tsx", "ts"] : ["tsx", "ts"],
  // A second build (the admin-off check) goes elsewhere, so it never replaces out/.
  distDir: process.env.STUDIO_DIST_DIR || ".next",
};

export default nextConfig;
