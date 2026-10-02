import { defineConfig } from "@playwright/test";

/**
 * Studio's end-to-end tests run against the real thing: `sandbox-cli studio`
 * serving the built UI (npm run build) in front of a sandboxd on its in-memory
 * backend, both started by e2e/global-setup.ts from ../bin (make build).
 */
export default defineConfig({
  testDir: "./e2e",
  globalSetup: "./e2e/global-setup.ts",
  globalTeardown: "./e2e/global-teardown.ts",
  use: { baseURL: "http://127.0.0.1:7181" },
  workers: 1,
});
