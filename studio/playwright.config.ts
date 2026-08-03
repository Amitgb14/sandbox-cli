  import { defineConfig } from "@playwright/test";

  export default defineConfig({
    testDir: "./e2e",
    use: { baseURL: "http://localhost:3100" },
    // Starts `next dev` for you and waits for it; reuses one you already have running.
    webServer: {
      command: "npm run dev",
      url: "http://localhost:3100",
      reuseExistingServer: true,
      timeout: 120_000,
    },
  });
