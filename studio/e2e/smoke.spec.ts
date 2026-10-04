import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "@playwright/test";

const { token } = JSON.parse(readFileSync(join(__dirname, ".state.json"), "utf8")) as { token: string };
const ROUTES = ["/", "/sandboxes/", "/launch/", "/snapshots/", "/agents/", "/volumes/", "/settings/"];

test("every screen renders with no console errors", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => m.type() === "error" && errors.push(m.text()));
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
  await page.getByRole("link", { name: created.id }).click();
  await expect(page.getByRole("heading", { name: created.id })).toBeVisible();
  await page.getByRole("tab", { name: "Overview" }).click();
  await expect(page.getByText("echo hi")).toBeVisible();
  await page.getByRole("tab", { name: "Logs" }).click();
  await expect(page.getByText("hi", { exact: true })).toBeVisible();
  await page.getByRole("tab", { name: "Events" }).click();
  await expect(page.getByText("process.exited")).toBeVisible();
  page.once("dialog", (d) => d.accept());
  await page.getByRole("button", { name: "Terminate" }).click();
  await expect(page).toHaveURL(/\/sandboxes\//);
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
  for (const name of ["Runs", "Review", "Fleet"]) {
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
  await page.getByRole("tab", { name: /Running/ }).click();
  await expect(page.getByText("filter-keep")).toBeVisible();
  await expect(page.getByText("filter-gone")).toHaveCount(0);
  await api(`/v1/sandboxes/${keep.id}`, "DELETE");
});
