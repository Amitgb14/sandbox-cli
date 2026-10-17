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

test("a sandbox's metrics are exported from its panel, the credential never shown again", async ({ page, request }) => {
  const sb = await (await api(request, "/v1/sandboxes", "POST", { name: "export-me" })).json();
  await page.goto(`/sandbox/?id=${sb.id}`);
  await page.getByRole("button", { name: "Export metrics" }).click();
  const dlg = page.getByRole("dialog", { name: "Metrics export" });
  await dlg.getByRole("textbox", { name: "OTLP endpoint" }).fill("https://otlp.collector.example");
  await dlg.getByRole("combobox", { name: "Push every" }).click();
  await page.getByRole("option", { name: "30 s" }).click();
  await dlg.getByRole("button", { name: "Add header" }).click();
  await dlg.getByLabel("Header value").fill("Bearer e2e-otel-secret");
  await dlg.getByRole("button", { name: "Save" }).click();
  await expect(dlg).toHaveCount(0);

  const got = await (await api(request, `/v1/sandboxes/${sb.id}`)).json();
  expect(got.metrics_export).toMatchObject({ endpoint: "https://otlp.collector.example/v1/metrics", interval_secs: 30, header_names: ["Authorization"] });
  expect(JSON.stringify(got)).not.toContain("e2e-otel-secret");
  await expect(page.getByText("https://otlp.collector.example/v1/metrics")).toBeVisible();
  await expect(page.getByText("every 30 s · headers Authorization")).toBeVisible();

  // Opened again, the header is there by name, its value left to be typed.
  await page.getByRole("button", { name: "Change the metrics export" }).click();
  await expect(dlg.getByRole("textbox", { name: "Header name" })).toHaveValue("Authorization");
  await expect(dlg.getByLabel("Header value")).toHaveValue("");
  await dlg.getByRole("button", { name: "Stop exporting" }).click();
  await expect(dlg).toHaveCount(0);
  expect((await (await api(request, `/v1/sandboxes/${sb.id}`)).json()).metrics_export).toBeUndefined();
});

test("the Playground's metrics export is in the run and its code, the header's value in neither", async ({ page, request }) => {
  await page.goto("/launch/");
  await page.getByText("Command", { exact: true }).first().click();
  await page.getByRole("textbox", { name: "Command" }).fill("echo exported");
  await page.getByText("Sandbox options").click();
  await page.getByRole("textbox", { name: "OTLP endpoint" }).fill("https://otlp.collector.example");
  await page.getByRole("button", { name: "Add header" }).click();
  await page.getByLabel("Header value").fill("Bearer e2e-launch-secret");
  await expect(page.getByText("to otlp.collector.example, every 1 min")).toBeVisible();
  const code = page.locator("pre").last();
  await expect(code).toContainText("--metrics-export https://otlp.collector.example --metrics-export-header Authorization=OTEL_AUTHORIZATION");
  await page.getByRole("tab", { name: "curl" }).click();
  await expect(code).toContainText('"metrics_export":{"endpoint":"https://otlp.collector.example","interval_secs":60,"headers":{"Authorization":"<Authorization value>"}}');
  await expect(page.locator("body")).not.toContainText("e2e-launch-secret");

  await page.locator("form").getByRole("button", { name: "Launch" }).click();
  await expect(page).toHaveURL(/\/sandbox\/?\?id=sbx_/);
  const id = new URL(page.url()).searchParams.get("id")!;
  const got = await (await api(request, `/v1/sandboxes/${id}`)).json();
  expect(got.metrics_export).toMatchObject({ endpoint: "https://otlp.collector.example/v1/metrics", interval_secs: 60, header_names: ["Authorization"] });
  expect(JSON.stringify(got)).not.toContain("e2e-launch-secret");
});
