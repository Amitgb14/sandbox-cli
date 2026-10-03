import type { NextConfig } from "next";

/**
 * Studio is a static export, embedded in sandbox-cli and served by
 * `sandbox-cli studio` from the same origin as its API — so there is no
 * server-side rendering, no API base URL to configure, and nothing read from
 * the environment. `make studio` builds it into internal/studio/ui.
 *
 * Trailing slashes make every route a directory with an index.html, which is
 * what a plain file server (Go's, here) serves without rewrites.
 */
const nextConfig: NextConfig = {
  output: "export",
  trailingSlash: true,
  images: { unoptimized: true },
};

export default nextConfig;
