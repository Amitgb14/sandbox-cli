import type { NextConfig } from "next";

/**
 * Studio is a control plane, not a brochure — it talks to a local daemon, so
 * unlike ../web it is a served Next app rather than a static export.
 *
 * The backend base URL is the one thing that differs between a developer
 * running `sandbox-cli studio` on their laptop and a packaged build, so it is
 * read from the environment with the local daemon as the default. See
 * src/lib/api/client.ts.
 */
const nextConfig: NextConfig = {
  env: {
    NEXT_PUBLIC_SANDBOX_API:
      process.env.NEXT_PUBLIC_SANDBOX_API ?? "http://localhost:8787",
  },
};

export default nextConfig;
