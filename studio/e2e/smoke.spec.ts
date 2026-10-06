import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "@playwright/test";

const { token } = JSON.parse(readFileSync(join(__dirname, ".state.json"), "utf8")) as { token: string };
const ROUTES = ["/", "/sandboxes/", "/launch/", "/snapshots/", "/agents/", "/volumes/", "/settings/"];

test("every screen renders with no console errors", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  // Except whoami's 404: it is how a plain sandboxd says it is not a gateway
  // (lib/caller.ts), and the browser logs every 4xx a page fetches.
  page.on("console", (m) => m.type() === "error" && !m.location().url.endsWith("/api/v1/whoami") && errors.push(m.text()));
  await page.goto(`/#token=${token}`);
  await expect(page.getByText("e2e · fake")).toBeVisible();
  expect(page.url()).not.toContain("token=");
  for (const r of ROUTES) {
    await page.goto(r);
    await expect(page.locator("main h1").first()).toBeVisible();
  }
  expect(errors).toEqual([]);
});

test("a tab without the token is asked for it, and nothing else works", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByText("Studio needs its token")).toBeVisible();
  await page.getByPlaceholder(/token=/).fill(`http://127.0.0.1:7181/#token=${token}`);
  await page.getByRole("button", { name: "Use" }).click();
  await expect(page.getByText("Studio needs its token")).toHaveCount(0);
});

test("a sandbox started elsewhere is listed, labelled, and has events", async ({ page, request }) => {
  const api = (path: string, init: { method?: string; data?: unknown } = {}) =>
    request.fetch(`/api${path}`, { method: init.method ?? "GET", data: init.data, headers: { Authorization: `Bearer ${token}` } });
  const created = await (await api("/v1/sandboxes", { method: "POST", data: { labels: { suite: "studio-e2e" } } })).json();
  await api(`/v1/sandboxes/${created.id}/run`, { method: "POST", data: { argv: ["echo", "hi"] } });

  await page.goto(`/#token=${token}`);
  // The page keeps the token once its first request runs; navigating before
  // that would leave a tab without it.
  await expect(page.getByText("e2e · fake")).toBeVisible();
  await page.goto("/sandboxes/");
  await expect(page.getByText("suite=studio-e2e")).toBeVisible();
  // A click on the row opens it in a panel beside the list.
  await page.getByRole("row").filter({ hasText: created.id }).getByText(created.id).click();
  const panel = page.getByRole("dialog");
  await expect(panel.getByRole("heading", { name: created.id })).toBeVisible();
  // A running sandbox can be connected to from here, as from sandbox-cli shell.
  await expect(panel.getByRole("button", { name: "Terminal", exact: true })).toBeVisible();
  await panel.getByRole("tab", { name: "Overview" }).click();
  await expect(panel.getByText("echo hi")).toBeVisible();
  // The fake backend takes no snapshots, and the overview says so rather than offering a schedule.
  await expect(panel.getByText("This endpoint takes no snapshots.")).toBeVisible();
  await panel.getByRole("tab", { name: "Logs" }).click();
  await expect(panel.getByText("hi", { exact: true })).toBeVisible();
  await panel.getByRole("tab", { name: "Events" }).click();
  await expect(panel.getByText("process.exited")).toBeVisible();

  // The same details, as a page of their own.
  await panel.getByRole("link", { name: "Open as a page" }).click();
  await expect(page).toHaveURL(/\/sandbox\/?\?id=sbx_/);
  await expect(page.getByRole("heading", { level: 1, name: created.id })).toBeVisible();
  // Wide, the overview is beside the tabs rather than one of them.
  await expect(page.getByRole("tab", { name: "Overview" })).toHaveCount(0);
  await expect(page.getByText("echo hi").first()).toBeVisible();
  page.once("dialog", (d) => d.accept());
  await page.getByRole("button", { name: "Terminate" }).click();
  await expect(page).toHaveURL(/\/sandboxes\//);
});

test("the list sums what sandboxes were given, walks them in its panel, and terminates a selection", async ({ page, request }) => {
  const api = (path: string, method = "GET", data?: unknown) =>
    request.fetch(`/api${path}`, { method, data, headers: { Authorization: `Bearer ${token}` } });
  const a = await (await api("/v1/sandboxes", "POST", { name: "walk-a", cpus: 2, memory_mb: 2048 })).json();
  const b = await (await api("/v1/sandboxes", "POST", { name: "walk-b" })).json();

  await page.goto(`/#token=${token}`);
  await expect(page.getByText("e2e · fake")).toBeVisible();
  await page.goto("/sandboxes/");
  // Allocations from the node, against its capacity.
  await expect(page.getByRole("meter", { name: "vCPU given" })).toBeVisible();
  await expect(page.getByRole("row").filter({ hasText: "walk-a" }).getByText("2 vCPU")).toBeVisible();
  // What it was given opens what it uses: the last hour, sampled on the host.
  await page.getByRole("button", { name: "Metrics of walk-a" }).click();
  const metrics = page.getByRole("dialog");
  await expect(metrics.getByRole("heading", { name: "Metrics of walk-a" })).toBeVisible();
  // Its charts, or, before the first sample is taken (every 10 s), how often one is.
  await expect(metrics.getByText(/^(CPU, % of 2 vCPU|No samples yet: one is taken every 10 seconds)/)).toBeVisible();
  // It stays open while the list polls (every 5 s) under it.
  await page.waitForResponse((r) => r.url().endsWith("/v1/sandboxes") && r.request().method() === "GET");
  await page.waitForResponse((r) => r.url().endsWith("/v1/sandboxes") && r.request().method() === "GET");
  await expect(metrics.getByRole("heading", { name: "Metrics of walk-a" })).toBeVisible();
  // A click inside it stays there, and does not open the row's panel too.
  await metrics.getByText("The last hour", { exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(1);
  await page.keyboard.press("Escape");
  await expect(metrics).toHaveCount(0);

  await page.getByPlaceholder(/Search/).fill("walk-");
  await page.getByRole("row").filter({ hasText: "walk-b" }).getByText("walk-b").click();
  const panel = page.getByRole("dialog");
  await expect(panel.getByRole("heading", { name: "walk-b" })).toBeVisible();
  // Newest first: walk-b, then walk-a.
  await panel.getByRole("button", { name: "Next sandbox" }).click();
  await expect(panel.getByRole("heading", { name: "walk-a" })).toBeVisible();
  await expect(panel.getByRole("button", { name: "Next sandbox" })).toBeDisabled();
  await panel.getByRole("button", { name: "Close" }).click();
  await expect(panel).toHaveCount(0);

  await page.getByRole("checkbox", { name: "Select all on this page" }).click();
  await expect(page.getByText("2 selected", { exact: true })).toBeVisible();
  page.once("dialog", (d) => d.accept());
  await page.getByRole("button", { name: "Terminate 2" }).click();
  await expect(page.getByText("Terminated 2")).toBeVisible();
  for (const s of [a, b]) {
    expect((await (await api(`/v1/sandboxes/${s.id}`)).json()).state).toBe("terminated");
  }
});

test("the Playground starts a sandbox from the image chosen, and says so in its code", async ({ page, request }) => {
  await page.goto(`/#token=${token}`);
  await expect(page.getByText("e2e · fake")).toBeVisible();
  await page.goto("/launch/");
  await page.getByText("Command", { exact: true }).first().click();
  await page.getByRole("textbox", { name: "Command" }).fill("echo from-an-image");
  // The fake backend takes no snapshots, so that choice is offered but off,
  // and so is a schedule.
  await expect(page.getByText("(this endpoint takes none)")).toBeVisible();
  await expect(page.getByRole("checkbox", { name: "Snapshot on a schedule" })).toBeDisabled();
  await expect(page.getByText("(this endpoint takes no snapshots)")).toBeVisible();
  await page.getByRole("combobox", { name: "Image" }).click();
  await page.getByPlaceholder("Search, or type any image…").fill("e2e-image:1");
  await page.getByRole("option", { name: /Use e2e-image:1/ }).click();
  await expect(page.getByRole("combobox", { name: "Image" })).toContainText("e2e-image:1");
  const code = page.locator("pre").last();
  await expect(code).toContainText("sandbox-cli run --image e2e-image:1 -- echo from-an-image");
  await page.getByRole("tab", { name: "curl" }).click();
  await expect(code).toContainText('"image":"e2e-image:1"');
  await page.locator("form").getByRole("button", { name: "Launch" }).click();
  await expect(page).toHaveURL(/\/sandbox\/?\?id=sbx_/);
  const id = new URL(page.url()).searchParams.get("id");
  const sb = await (await request.fetch(`/api/v1/sandboxes/${id}`, { headers: { Authorization: `Bearer ${token}` } })).json();
  expect(sb.image).toBe("e2e-image:1");
});

test("the Playground lists agents with what each needs to log in", async ({ page }) => {
  await page.goto(`/#token=${token}`);
  await expect(page.getByText("e2e · fake")).toBeVisible();
  await page.goto("/launch/");
  const agents = page.getByRole("radiogroup", { name: "Agent" });
  await agents.getByRole("radio", { name: /codex/ }).click();
  await expect(agents.getByRole("radio", { name: /codex/ })).toHaveAttribute("aria-checked", "true");
  await expect(page.locator("pre").last()).toContainText("sandbox-cli agent codex");
  // Opened, a row says where the login is kept, which keys it takes, and what it reaches.
  await page.getByRole("button", { name: "Show claude's login details" }).click();
  await expect(agents.getByText("~/.claude/.credentials.json")).toBeVisible();
  await expect(agents.getByText("ANTHROPIC_API_KEY")).toBeVisible();
  await expect(agents.getByText("api.anthropic.com").first()).toBeVisible();
});

test("the Desktop tab explains a sandbox whose image has no desktop", async ({ page, request }) => {
  const api = (path: string, method = "GET", data?: unknown) =>
    request.fetch(`/api${path}`, { method, data, headers: { Authorization: `Bearer ${token}` } });
  // The fake backend runs builtins only, so sandbox-desktop is "no such command" there,
  // as it is in any image but the desktop one.
  const sb = await (await api("/v1/sandboxes", "POST", { name: "no-desktop" })).json();
  await page.goto(`/#token=${token}`);
  await expect(page.getByText("e2e · fake")).toBeVisible();
  await page.goto("/sandboxes/");
  await page.getByRole("row").filter({ hasText: "no-desktop" }).getByText("no-desktop").click();
  const panel = page.getByRole("dialog");
  await panel.getByRole("tab", { name: "Desktop" }).click();
  await expect(panel.getByText("No desktop is running in this sandbox.", { exact: false })).toBeVisible();
  await panel.getByRole("button", { name: "Start desktop" }).click();
  await expect(panel.getByText("image has no desktop", { exact: false })).toBeVisible();
  await expect(panel.locator("pre").last()).toContainText("--image");
  await expect(panel.locator("pre").last()).toContainText("sandbox-desktop");
  await api(`/v1/sandboxes/${sb.id}`, "DELETE");
});

test("Agents lists only the verified agents, and Launch starts a sandbox", async ({ page }) => {
  await page.goto(`/#token=${token}`);
  await expect(page.getByText("e2e · fake")).toBeVisible();
  await page.goto("/agents/");
  for (const name of ["claude", "cline", "codex", "gemini", "opencode"]) {
    await expect(page.locator("main").getByText(name, { exact: true })).toBeVisible();
  }
  // Interactive-only agents stay in the CLI.
  await expect(page.locator("main").getByText("goose", { exact: true })).toHaveCount(0);

  await page.goto("/launch/");
  await page.getByText("Command", { exact: true }).first().click();
  await page.getByRole("textbox", { name: "Command" }).fill("echo from-studio");
  await page.locator("form").getByRole("button", { name: "Launch" }).click();
  await expect(page).toHaveURL(/\/sandbox\/?\?id=sbx_/);
});

test("the repository screens are gone", async ({ page, request }) => {
  await page.goto(`/#token=${token}`);
  await expect(page.getByText("e2e · fake")).toBeVisible();
  const nav = page.locator("[data-sidebar=sidebar]");
  // Home is the sandbox list, and the sidebar is the screens there are.
  await expect(page.locator("main h1")).toHaveText("Sandboxes");
  for (const name of ["Runs", "Review", "Fleet", "Overview", "Dashboard"]) {
    await expect(nav.getByRole("link", { name, exact: true })).toHaveCount(0);
  }
  for (const path of ["/api/repos", "/api/runs"]) {
    const r = await request.fetch(path, { headers: { Authorization: `Bearer ${token}` } });
    expect(r.status(), path).toBeGreaterThanOrEqual(400);
  }
});

test("the Playground writes the same sandbox as code, and the list filters by state", async ({ page, request }) => {
  await page.goto(`/#token=${token}`);
  await expect(page.getByText("e2e · fake")).toBeVisible();
  await page.goto("/launch/");
  await page.getByText("Command", { exact: true }).first().click();
  await page.getByRole("textbox", { name: "Command" }).fill("make test");
  const code = page.locator("pre").last();
  await expect(code).toContainText("sandbox-cli run -- make test");
  await page.getByRole("tab", { name: "Python" }).click();
  await expect(code).toContainText('c.run(sb["id"], ["make", "test"])');
  await page.getByRole("tab", { name: "TypeScript" }).click();
  await expect(code).toContainText('await c.run(sb.id, ["make","test"])');

  // One running and one terminated sandbox; the state filter tells them apart.
  const api = (path: string, method = "GET", data?: unknown) =>
    request.fetch(`/api${path}`, { method, data, headers: { Authorization: `Bearer ${token}` } });
  const keep = await (await api("/v1/sandboxes", "POST", { name: "filter-keep" })).json();
  const gone = await (await api("/v1/sandboxes", "POST", { name: "filter-gone" })).json();
  await api(`/v1/sandboxes/${gone.id}`, "DELETE");
  await page.goto("/sandboxes/");
  await page.getByPlaceholder(/Search/).fill("filter-");
  // Live sandboxes only, until asked for the rest.
  await expect(page.getByText("filter-keep")).toBeVisible();
  await expect(page.getByText("filter-gone")).toHaveCount(0);
  await page.getByRole("button", { name: /^State/ }).click();
  await page.getByRole("option", { name: /Terminated/ }).click();
  await page.keyboard.press("Escape");
  await expect(page.getByText("filter-gone")).toBeVisible();
  await api(`/v1/sandboxes/${keep.id}`, "DELETE");
});
