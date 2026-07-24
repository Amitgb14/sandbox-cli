import type { NextConfig } from "next";

/**
 * Static export: `next build` emits a fully static site into ./out, so the page
 * deploys to GitHub Pages / any bucket with no Node server — same deployment
 * story as the hand-written page it replaces.
 */
const nextConfig: NextConfig = {
  output: "export",
  images: { unoptimized: true },
  trailingSlash: true,
};

export default nextConfig;
