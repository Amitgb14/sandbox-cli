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

test("a running sandbox is renamed, relabelled, retimed and given a new network from its panel", async ({ page, request }) => {
  const sb = await (await api(request, "/v1/sandboxes", "POST", { name: "edit-me", labels: { team: "a", ticket: "1" } })).json();
  await api(request, "/egress/groups/edit-go", "PUT", { hosts: ["proxy.golang.org"] });

  await page.goto(`/sandbox/?id=${sb.id}`);
  await page.getByRole("button", { name: "Edit name, labels and idle timeout" }).first().click();
  const dlg = page.getByRole("dialog", { name: "Edit edit-me" });
  await dlg.getByRole("textbox", { name: "Name" }).fill("edited");
  await dlg.getByRole("textbox", { name: "Label 1 value" }).fill("b");
  await dlg.getByRole("button", { name: "Remove label ticket" }).click();
  await dlg.getByRole("button", { name: "Add label" }).click();
  await dlg.getByRole("textbox", { name: "Label 2 key" }).fill("env");
  await dlg.getByRole("textbox", { name: "Label 2 value" }).fill("dev");
  await dlg.getByRole("combobox", { name: "Idle auto-stop" }).click();
  await page.getByRole("option", { name: "after 2 h idle" }).click();
  await dlg.getByRole("button", { name: "Save" }).click();
  await expect(dlg).toHaveCount(0);
  let got = await (await api(request, `/v1/sandboxes/${sb.id}`)).json();
  expect([got.name, got.labels, got.idle_timeout_secs]).toEqual(["edited", { team: "b", env: "dev" }, 7200]);

  await page.getByRole("button", { name: "Change network" }).click();
  const net = page.getByRole("dialog", { name: "Change network" });
  await net.getByRole("radio", { name: "allowlist" }).click();
  await net.getByRole("checkbox", { name: "Group edit-go" }).click();
  await net.getByRole("checkbox", { name: "Include the built-in hosts" }).click();
  await net.getByRole("textbox", { name: "Hosts" }).fill("");
  await net.getByRole("button", { name: "Apply" }).click();
  await expect(net).toHaveCount(0);
  got = await (await api(request, `/v1/sandboxes/${sb.id}`)).json();
  expect(got.network).toMatchObject({ mode: "allowlist", allow: ["proxy.golang.org"] });

  // The fake takes no disk snapshots: Resize says why rather than trying.
  const resize = page.getByRole("button", { name: "Resize" });
  await expect(resize).toHaveAttribute("aria-disabled", "true");
  await resize.hover();
  await expect(page.getByRole("tooltip")).toContainText("a running VM's vCPUs and memory are fixed");

  await api(request, `/v1/sandboxes/${sb.id}`, "DELETE");
  await api(request, "/egress/groups/edit-go", "DELETE");
});
