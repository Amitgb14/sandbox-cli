import type { NextConfig } from "next";

/**
 * Studio is a control plane, not a brochure — it talks to a local daemon, so
 * unlike ../web it is a served Next app rather than a static export.
 *
 * The backend base URL is the one thing that differs between a developer
 * running `sandbox-cli studio` on their laptop and a packaged build, so it is
 * read from the environment with the local daemon as the default. See
 * src/lib/api/client.ts.
 *
 * `env` here is the *build-time* value, which is all a developer needs. A
 * published image is told at `docker run` time instead — `SANDBOX_API_URL`,
 * resolved per request by `apiBase()` — because the port a user's daemon ends up
 * on cannot be known when the image is built.
 *
 * `output: "standalone"` is what makes that image small enough to be worth
 * pulling: Next traces the server's actual imports and emits a self-contained
 * directory, so the runtime layer carries neither the source nor the full
 * node_modules. It costs a developer nothing — `next dev` ignores it, and
 * `next build` just writes one more directory.
 */
const nextConfig: NextConfig = {
  output: "standalone",
  env: {
    NEXT_PUBLIC_SANDBOX_API:
      process.env.NEXT_PUBLIC_SANDBOX_API ?? "http://localhost:8787",
  },
};

export default nextConfig;
