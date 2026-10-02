import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "@playwright/test";

const { token } = JSON.parse(readFileSync(join(__dirname, ".state.json"), "utf8")) as { token: string };
const ROUTES = ["/", "/sandboxes/", "/launch/", "/runs/", "/review/", "/fleet/", "/agents/", "/volumes/", "/settings/"];

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
  await page.getByRole("tab", { name: "Output" }).click();
  await expect(page.getByText("hi", { exact: true })).toBeVisible();
  await page.getByRole("tab", { name: "Events" }).click();
  await expect(page.getByText("process.exited")).toBeVisible();
  page.once("dialog", (d) => d.accept());
  await page.getByRole("button", { name: "Terminate" }).click();
  await expect(page).toHaveURL(/\/sandboxes\//);
});
