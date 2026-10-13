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

  // The endpoint resizes in place (capability resize): the same sandbox,
  // restarted at its new size; a smaller disk is not offered.
  await page.getByRole("button", { name: "Resize" }).click();
  const rz = page.getByRole("dialog", { name: /^Resize/ });
  await rz.getByLabel("Disk (MiB)").fill(String(got.disk_mb - 1));
  await expect(rz.getByText(/Disk: only grows/)).toBeVisible();
  await expect(rz.getByRole("button", { name: "Resize and restart" })).toBeDisabled();
  await rz.getByLabel("vCPUs").fill("2");
  await rz.getByLabel("Memory (MiB)").fill("2048");
  await rz.getByLabel("Disk (MiB)").fill(String(got.disk_mb + 1024));
  await rz.getByRole("button", { name: "Resize and restart" }).click();
  await expect(rz).toHaveCount(0);
  const before = got.disk_mb;
  got = await (await api(request, `/v1/sandboxes/${sb.id}`)).json();
  expect([got.cpus, got.memory_mb, got.disk_mb]).toEqual([2, 2048, before + 1024]);

  // A larger disk alone is grown live: no restart, so the button says so.
  await page.getByRole("button", { name: "Resize" }).click();
  const grow = page.getByRole("dialog", { name: /^Resize/ });
  await grow.getByLabel("Disk (MiB)").fill(String(got.disk_mb + 1024));
  await grow.getByRole("button", { name: "Grow disk" }).click();
  await expect(grow).toHaveCount(0);
  await expect(page.getByText(/grown live/)).toBeVisible();
  const grown = await (await api(request, `/v1/sandboxes/${sb.id}`)).json();
  expect([grown.cpus, grown.memory_mb, grown.disk_mb]).toEqual([2, 2048, got.disk_mb + 1024]);

  await api(request, `/v1/sandboxes/${sb.id}`, "DELETE");
  await api(request, "/egress/groups/edit-go", "DELETE");
});
