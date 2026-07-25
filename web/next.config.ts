import type { NextConfig } from "next";

/**
 * Static export: `next build` emits a fully static site into ./out, so the
 * landing page deploys to GitHub Pages or any bucket with no Node server.
 */
const nextConfig: NextConfig = {
  output: "export",
  images: { unoptimized: true },
  trailingSlash: true,
};

export default nextConfig;
