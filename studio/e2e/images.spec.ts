import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "@playwright/test";

const { token } = JSON.parse(readFileSync(join(__dirname, ".state.json"), "utf8")) as { token: string };

test("an image is downloaded, started from and removed from Images, and a failed one says why", async ({ page }) => {
  await page.goto(`/#token=${token}`);
  await expect(page.getByText("e2e · fake")).toBeVisible();
  await page.getByRole("link", { name: "Images", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Images", level: 1 })).toBeVisible();

  const ref = "registry.example/e2e/tool:1.0";
  await page.getByRole("button", { name: "Download image" }).click();
  await page.getByRole("textbox", { name: "Image" }).fill(ref);
  await page.getByRole("button", { name: "Download", exact: true }).click();
  const list = page.getByRole("list", { name: "Images" });
  const row = list.getByRole("listitem").filter({ hasText: ref });
  await expect(row.getByText("installed", { exact: true })).toBeVisible();
  await expect(row.getByText(/^64(\.0)? MiB$/)).toBeVisible();

  await row.getByRole("link", { name: `Start a sandbox from ${ref}` }).click();
  await expect(page).toHaveURL(/\/launch\/?\?image=/);
  await expect(page.getByRole("combobox", { name: "Image" })).toContainText("tool:1.0");

  await page.goto("/images/");
  await row.getByRole("button", { name: `Remove ${ref}` }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Remove", exact: true }).click();
  await expect(row).toHaveCount(0);

  const missing = "registry.example/e2e/does-not-exist:1";
  await page.getByRole("button", { name: "Download image" }).click();
  await page.getByRole("textbox", { name: "Image" }).fill(missing);
  await page.getByRole("button", { name: "Download", exact: true }).click();
  const bad = list.getByRole("listitem").filter({ hasText: missing });
  await expect(bad.getByText("failed", { exact: true })).toBeVisible();
  await expect(bad.getByText("manifest unknown")).toBeVisible();
  await bad.getByRole("button", { name: `Clear ${missing}` }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Clear", exact: true }).click();
  await expect(bad).toHaveCount(0);
});
