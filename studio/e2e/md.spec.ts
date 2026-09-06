import { test, expect, type Page } from "@playwright/test";

/**
 * The console renders an agent's markdown, and renders nothing an agent can turn
 * into markup. Issue #151.
 *
 * Hermetic: every route the run detail asks for is stubbed here, so this needs
 * no daemon and no container — which matters, because the input under test is
 * *hostile* text and the point is to write it by hand rather than hope an agent
 * emits it. The health stub is what puts the client in live mode; without it
 * every read is served from a fixture and no request is made for a route to
 * intercept.
 */
const RUN = "sandbox-cli-82799c04-md";

const CONVERSATION = [
  {
    role: "user",
    text: "Summarise what you changed.",
    at: "2026-09-06T10:00:00Z",
  },
  {
    role: "assistant",
    at: "2026-09-06T10:00:20Z",
    text: [
      "## What changed",
      "",
      "Rewrote the **parser** and left `parseBlocks` pure.",
      "",
      "- one item",
      "- two item",
      "",
      "```go",
      'fmt.Println("hi")',
      "```",
      "",
      "See [the docs](https://example.com/docs) for the rest.",
      "",
      "Click [here](javascript:alert(1)) or [there](data:text/html,<b>x</b>).",
      "",
      "![tracker](https://tracker.example/pixel.png)",
      "",
      '<img src=x onerror="alert(1)"> and <b>not bold</b>',
    ].join("\n"),
  },
];

async function stub(page: Page) {
  await page.route("**/v1/health*", (r) =>
    r.fulfill({
      json: {
        version: "0.0.0-test",
        engine: "docker",
        engineVersion: "27.0.0",
        profile: "dev",
        egress: { mode: "allowlist", baseline: true, domains: 3 },
        host: { os: "linux", arch: "arm64", cpus: 4, memBytes: 8589934592 },
        authRequired: false,
      },
    }),
  );
  await page.route(`**/v1/runs/${RUN}/conversation*`, (r) =>
    r.fulfill({ json: { messages: CONVERSATION, writable: false } }),
  );
  await page.route(`**/v1/runs/${RUN}`, (r) =>
    r.fulfill({
      json: {
        id: RUN,
        name: `sandbox-${RUN}`,
        kind: "interactive",
        state: "running",
        exitCode: null,
        createdAt: "2026-09-06T10:00:00Z",
        startedAt: "2026-09-06T10:00:00Z",
        finishedAt: null,
        durationMs: null,
        agent: "claude",
        command: ["claude"],
        image: "sandbox-base:test",
        engine: "docker",
        workspace: "/tmp/repo",
        workdir: "/workspace",
        repoId: "sandbox-cli-82799c04",
        repoName: "sandbox-cli",
        branch: "md",
        base: null,
        verify: null,
        profile: "dev",
        network: {
          mode: "allowlist",
          baseline: true,
          allow: [],
          ingressPorts: [],
        },
        security: {
          noNewPrivileges: true,
          capDrop: ["ALL"],
          capAdd: [],
          pidsLimit: 1024,
          memory: "",
          cpus: "",
          seccomp: "",
          user: "sandbox",
          hardening: true,
        },
        mounts: [],
        envNames: [],
        detached: false,
        tty: true,
        openStdin: true,
      },
    }),
  );
  // Anything else the detail page reaches for: an empty answer beats a spinner.
  await page.route("**/v1/runs/*/metrics*", (r) =>
    r.fulfill({ json: { samples: [] } }),
  );
  await page.route("**/v1/runs/*/logs*", (r) => r.fulfill({ json: [] }));
  await page.route("**/v1/runs/*/diff*", (r) => r.fulfill({ json: [] }));
}

test("an agent's markdown is rendered, and its markup is not", async ({
  page,
}) => {
  // A request for any of these means the page tried to load something an agent
  // named. Nothing in a rendered reply may cause one — an image in a transcript
  // is a beacon that reports when somebody read it.
  const fetched: string[] = [];
  page.on("request", (r) => {
    if (/tracker\.example|^data:/.test(r.url())) fetched.push(r.url());
  });

  await stub(page);
  await page.goto(`/runs/${RUN}`);
  await page.getByRole("tab", { name: "Console" }).click();

  // Scoped to what this renderer emitted, not to the page: `main` carries the
  // app's own lists and links, and an assertion counting those proves nothing.
  const panel = page.locator("[data-agent-markdown]").last();
  await expect(panel.getByText("What changed")).toBeVisible();

  // Formatted, not literal: the markers are gone and the elements are there.
  await expect(panel.locator("strong", { hasText: "parser" })).toBeVisible();
  await expect(panel.locator("code", { hasText: "parseBlocks" })).toBeVisible();
  await expect(panel.locator("li")).toHaveCount(2);
  await expect(panel.locator("pre code")).toContainText('fmt.Println("hi")');
  await expect(panel.getByText("**parser**")).toHaveCount(0);

  // An http(s) link is a link, and carries the rel that keeps the opened page
  // from reaching back through window.opener.
  const link = panel.getByRole("link", { name: "the docs" });
  await expect(link).toHaveAttribute("href", "https://example.com/docs");
  await expect(link).toHaveAttribute("rel", /noopener/);

  // Everything else is not a link. The label and the URL both stay visible, so a
  // reader sees the claim rather than a label that lies about where it goes.
  await expect(panel.getByRole("link", { name: "here" })).toHaveCount(0);
  await expect(panel.getByRole("link", { name: "there" })).toHaveCount(0);
  await expect(panel.getByText("javascript:alert(1)")).toBeVisible();

  // No markup, ever: the agent's tags are characters on the screen and no
  // element of their own.
  await expect(panel.locator("img")).toHaveCount(0);
  await expect(panel.locator("b")).toHaveCount(0);
  await expect(panel.getByText("<b>not bold</b>")).toBeVisible();
  await expect(panel.getByText("onerror=")).toBeVisible();

  expect(fetched, "a rendered reply fetched something an agent named").toEqual(
    [],
  );
});
