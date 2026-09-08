import { test, expect, type Page } from "@playwright/test";

/**
 * Scrolling back in the attached terminal, in both buffers. Issue #152.
 *
 * The report was that the terminal cannot be scrolled. Measured, it is two
 * different behaviours and both are correct:
 *
 *   - In the **normal** buffer the viewport scrolls, out of the 5000-line
 *     scrollback the terminal is configured with.
 *   - In the **alternate** buffer — which is what a full-screen agent switches
 *     to on startup — there is no scrollback to move through, in this or any
 *     other terminal, so the wheel is forwarded to the application as arrow
 *     keys instead and the *agent* decides what to scroll.
 *
 * The second half is what this file exists to pin, because it depends on
 * something that reads like it should have nothing to do with scrolling.
 * xterm only sends those arrows `if (!s.wheel)` — if the application has not
 * taken over the wheel through mouse tracking. This component swallows mouse
 * modes at the parser so that selection keeps working, and *that* is what leaves
 * the wheel free to become arrow keys. Granting mouse tracking again would make
 * the wheel a mouse report, which `isMouseReport` drops on the way out, and the
 * wheel would do nothing at all in the buffer where it is the only thing there
 * is.
 *
 * The stream is held open from inside the page rather than fulfilled with a
 * body: a finite body *ends* the stream, and the component disposes its
 * `onData` handler in the `finally`, so nothing is sent no matter what is typed
 * — which is exactly the false negative the first version of this test hit.
 */
const RUN = "sandbox-cli-82799c04-live";

async function stub(page: Page, payload: string) {
  await page.addInitScript(
    ({ run, payload }) => {
      const real = window.fetch.bind(window);
      window.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
        const url = typeof input === "string" ? input : input.toString();
        if (
          url.includes(`/v1/runs/${run}/console`) &&
          !url.includes("/console/")
        ) {
          const b64 = btoa(payload);
          const body = new ReadableStream({
            start(c) {
              c.enqueue(
                new TextEncoder().encode(`data: ${JSON.stringify(b64)}\n\n`),
              );
              // and never closes, which is the point
            },
          });
          return Promise.resolve(
            new Response(body, {
              status: 200,
              headers: { "content-type": "text/event-stream" },
            }),
          );
        }
        return real(input as RequestInfo, init);
      }) as typeof window.fetch;
    },
    { run: RUN, payload },
  );

  await page.route("**/v1/health*", (r) =>
    r.fulfill({
      json: {
        version: "0.0.0-test",
        engine: "docker",
        engineVersion: "27.0.0",
        profile: "dev",
        egress: { mode: "allowlist", baseline: true, domains: 3 },
        host: { os: "linux", arch: "arm64", cpus: 4, memBytes: 8589934592 },
        authRequired: true,
      },
    }),
  );
  await page.route(`**/v1/runs/${RUN}`, (r) =>
    r.fulfill({
      json: {
        id: RUN,
        name: `sandbox-${RUN}`,
        kind: "interactive",
        state: "running",
        exitCode: null,
        createdAt: "2026-09-08T10:00:00Z",
        startedAt: "2026-09-08T10:00:00Z",
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
        branch: "live",
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
  await page.route("**/v1/runs/*/metrics*", (r) =>
    r.fulfill({ json: { samples: [] } }),
  );
  await page.route("**/v1/runs/*/logs*", (r) => r.fulfill({ json: [] }));
  await page.route("**/v1/runs/*/diff*", (r) => r.fulfill({ json: [] }));
  await page.route("**/v1/runs/*/console/resize*", (r) =>
    r.fulfill({ status: 204, body: "" }),
  );
  await page.route("**/v1/runs/*/console/input*", (r) =>
    r.fulfill({ status: 204, body: "" }),
  );
}

const ESC = String.fromCharCode(27);
const LINES = Array.from({ length: 400 }, (_, i) => `line ${i + 1}`).join(
  "\r\n",
);

async function attach(page: Page, payload: string) {
  const sent: string[] = [];
  page.on("request", (r) => {
    if (r.url().includes("/console/input")) sent.push(r.postData() ?? "(none)");
  });
  await stub(page, payload);
  await page.goto("/");
  await page.evaluate(() => localStorage.setItem("sandbox-studio-token", "t"));
  await page.goto(`/runs/${RUN}`);
  await page.getByRole("tab", { name: "Terminal" }).click();
  await page.getByRole("button", { name: "Attach" }).first().click();
  await page.waitForSelector(".xterm-screen", { timeout: 20000 });
  await page.waitForTimeout(2000);

  const top = () =>
    page.evaluate(
      () =>
        (document.querySelector(".xterm-rows > div") as HTMLElement | null)
          ?.innerText ?? "",
    );

  const before = await top();
  // Control first: typing must be seen, or nothing else here means anything.
  await page.locator(".xterm-helper-textarea").focus();
  await page.keyboard.type("k");
  await page.waitForTimeout(700);
  const afterType = sent.length;

  await page.locator(".xterm-screen").hover();
  await page.mouse.wheel(0, -400);
  await page.waitForTimeout(800);

  return {
    before,
    after: await top(),
    typed: sent.slice(0, afterType),
    wheel: sent.slice(afterType),
  };
}

test("the normal buffer scrolls locally, and sends nothing", async ({
  page,
}) => {
  const r = await attach(page, LINES);

  // The control: typing reaches the agent, so an empty wheel result below would
  // mean the wheel sent nothing rather than that the probe is broken.
  expect(r.typed).toHaveLength(1);
  expect(r.typed[0]).toContain('"data":"k"');

  // Scrollback exists here, so the viewport moves and the agent is not involved.
  expect(r.before).not.toBe(r.after);
  expect(r.before).toMatch(/^line \d+$/);
  expect(
    r.wheel,
    "the wheel talked to the agent in the normal buffer",
  ).toHaveLength(0);
});

test("the alternate buffer has no scrollback, so the wheel becomes arrow keys", async ({
  page,
}) => {
  const r = await attach(page, ESC + "[?1049h" + LINES);

  expect(r.typed).toHaveLength(1);

  // Nothing to scroll to — the alternate buffer is one screen tall, here and in
  // every other terminal — so the view does not move.
  expect(r.after).toBe(r.before);

  // And the wheel is handed to the agent as an arrow key, which is the whole of
  // what a terminal can do for a full-screen application. If mouse tracking were
  // ever granted this becomes a mouse report, isMouseReport drops it, and the
  // wheel does nothing at all.
  expect(
    r.wheel.length,
    "the wheel was not forwarded to the agent",
  ).toBeGreaterThan(0);
  expect(r.wheel.join(""), "expected a cursor-up sequence").toContain(
    "\\u001b[A",
  );
});
