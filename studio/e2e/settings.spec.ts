import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test, type APIRequestContext } from "@playwright/test";

const { token } = JSON.parse(readFileSync(join(__dirname, ".state.json"), "utf8")) as { token: string };

const api = (request: APIRequestContext, path: string, method = "GET", data?: unknown) =>
  request.fetch(`/api${path}`, { method, data, headers: { Authorization: `Bearer ${token}` } });

test.beforeEach(async ({ page }) => {
  await page.goto(`/#token=${token}`);
  await expect(page.getByText("e2e · fake")).toBeVisible();
});

test("a template is made in Templates, and the Playground launches at its size", async ({ page, request }) => {
  await page.goto("/templates/");
  for (const name of ["micro", "small", "medium", "large", "xlarge"]) {
    await expect(page.locator("main").getByText(name, { exact: true })).toBeVisible();
  }
  await page.getByRole("button", { name: "New template" }).click();
  await page.getByLabel("Name").fill("ci-box");
  await page.getByLabel("vCPUs").fill("2");
  await page.getByLabel("Memory (MiB)").fill("3072");
  await page.getByRole("button", { name: "Save template" }).click();
  await expect(page.locator("main").getByText("ci-box", { exact: true })).toBeVisible();

  await page.getByRole("link", { name: "Launch with ci-box" }).click();
  await expect(page).toHaveURL(/\/launch\/?\?template=ci-box/);
  await expect(page.getByRole("radio", { name: /ci-box/ })).toBeChecked();
  await page.getByText("Command", { exact: true }).first().click();
  await page.getByRole("textbox", { name: "Command" }).fill("echo sized");
  await expect(page.locator("pre").last()).toContainText("sandbox-cli run --cpus 2 --memory 3072 -- echo sized");
  await page.locator("form").getByRole("button", { name: "Launch" }).click();
  await expect(page).toHaveURL(/\/sandbox\/?\?id=sbx_/);
  const id = new URL(page.url()).searchParams.get("id");
  const sb = await (await api(request, `/v1/sandboxes/${id}`)).json();
  expect([sb.cpus, sb.memory_mb]).toEqual([2, 3072]);
  await api(request, `/v1/sandboxes/${id}`, "DELETE");

  // A built-in one cannot be removed; yours can.
  await page.goto("/templates/");
  await expect(page.getByRole("button", { name: "Delete micro" })).toHaveCount(0);
  page.once("dialog", (d) => d.accept());
  await page.getByRole("button", { name: "Delete ci-box" }).click();
  await expect(page.locator("main").getByText("ci-box", { exact: true })).toHaveCount(0);
});

test("allowlist groups are made in Settings and picked in the Playground", async ({ page, request }) => {
  await page.goto("/settings/");
  await page.getByRole("button", { name: "New group" }).click();
  await page.getByRole("textbox", { name: "Group name" }).fill("go");
  await page.getByRole("button", { name: "Create group" }).click();
  const go = page.getByRole("group", { name: "Group go" });
  await go.getByRole("textbox", { name: "Add a host to go" }).fill("https://bad");
  await expect(go.getByText("A host name, or *.name")).toBeVisible();
  await go.getByRole("textbox", { name: "Add a host to go" }).fill("Proxy.Golang.org, sum.golang.org");
  await go.getByRole("button", { name: "Add" }).click();
  await expect(go.getByRole("list", { name: "go hosts" }).getByText("sum.golang.org")).toBeVisible();
  await expect(go.getByText("2 hosts")).toBeVisible();

  // A deny rule, for every launch.
  await page.getByRole("textbox", { name: "Host to deny" }).fill("evil.example.com");
  await page.getByRole("button", { name: "Deny", exact: true }).click();
  await expect(page.getByRole("list", { name: "Deny rules" }).getByText("evil.example.com")).toBeVisible();

  await page.goto("/launch/");
  await page.getByText("Command", { exact: true }).first().click();
  await page.getByRole("textbox", { name: "Command" }).fill("true");
  await page.getByRole("radio", { name: /Allowlist/ }).click();
  await page.getByRole("checkbox", { name: "Group go" }).click();
  await page.getByRole("checkbox", { name: "Include the built-in hosts" }).click();
  const code = page.locator("pre").last();
  await expect(code).toContainText("--network allowlist --allow proxy.golang.org --allow sum.golang.org --deny evil.example.com --no-baseline");
  await page.getByText(/^Reaches 2 hosts/).click();
  await page.locator("form").getByRole("button", { name: "Launch" }).click();
  await expect(page).toHaveURL(/\/sandbox\/?\?id=sbx_/);
  const id = new URL(page.url()).searchParams.get("id");
  const sb = await (await api(request, `/v1/sandboxes/${id}`)).json();
  expect(sb.network.mode).toBe("allowlist");
  expect([...sb.network.allow].sort()).toEqual(["proxy.golang.org", "sum.golang.org"]);
  expect(sb.network.deny).toEqual(["evil.example.com"]);
  await api(request, `/v1/sandboxes/${id}`, "DELETE");

  // Clean up for the other tests.
  await api(request, "/egress", "PUT", { rules: [] });
  await api(request, "/egress/groups/go", "DELETE");
});

test("an agent's API key is saved from Agents, and never shown back", async ({ page, request }) => {
  const secret = "sk-e2e-never-shown-0123456789";
  await page.goto("/agents/");
  await page.getByRole("button", { name: "Show codex's details" }).click();
  await page.getByRole("button", { name: "Add OPENAI_API_KEY" }).click();
  await page.getByRole("textbox", { name: "OPENAI_API_KEY value" }).fill(secret);
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByRole("button", { name: "Edit OPENAI_API_KEY" })).toBeVisible();
  const body = await (await api(request, "/agents")).text();
  expect(body).not.toContain(secret);
  expect(await page.content()).not.toContain(secret);
  page.once("dialog", (d) => d.accept());
  await page.getByRole("button", { name: "Remove OPENAI_API_KEY" }).click();
  await expect(page.getByRole("button", { name: "Add OPENAI_API_KEY" })).toBeVisible();
});
