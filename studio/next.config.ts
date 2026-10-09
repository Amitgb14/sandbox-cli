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

// NEXT_PUBLIC_STUDIO_HOSTED=on builds the Studio `sandbox-cli studio host`
// serves to many users: sign-in by invite link and a session cookie, no
// Studio token, nothing of the host. Its users are never operators, so it
// never carries the admin screens (`npm run build:hosted`, checked by
// scripts/check-hosted.mjs).
if (process.env.NEXT_PUBLIC_STUDIO_HOSTED === "on" && admin) {
  throw new Error("a hosted build (NEXT_PUBLIC_STUDIO_HOSTED=on) needs NEXT_PUBLIC_STUDIO_ADMIN=off");
}

const nextConfig: NextConfig = {
  // Always defined, so the minifier sees a literal on both sides of every
  // hosted comparison and drops the other build's code from each bundle; an
  // unset NEXT_PUBLIC_ variable is left as a run-time lookup instead.
  env: { NEXT_PUBLIC_STUDIO_HOSTED: process.env.NEXT_PUBLIC_STUDIO_HOSTED === "on" ? "on" : "off" },
  output: "export",
  trailingSlash: true,
  images: { unoptimized: true },
  pageExtensions: admin ? ["admin.tsx", "tsx", "ts"] : ["tsx", "ts"],
  // A second build (the admin-off check) goes elsewhere, so it never replaces out/.
  distDir: process.env.STUDIO_DIST_DIR || ".next",
  // noVNC (the Desktop tab) awaits at the top level of a module. Every browser
  // Studio runs in has async functions, but webpack's default target does not
  // say so, and it warned that the output might not run. Saying so is what
  // makes the generated code right, not just quiet.
  webpack(config) {
    config.output.environment = { ...config.output.environment, asyncFunction: true };
    return config;
  },
};

export default nextConfig;
