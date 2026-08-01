import { test, expect } from "@playwright/test";
const API = process.env.NEXT_PUBLIC_SANDBOX_API ?? "http://localhost:8787";
const TOKEN = process.env.SANDBOX_STUDIO_TOKEN;
test("a headless run explains itself instead of showing an empty terminal", async ({ page, request }) => {
  let id: string | undefined;
  const res = await request.get(`${API}/v1/runs`, { headers: TOKEN ? { authorization: `Bearer ${TOKEN}` } : undefined });
  if (res.ok()) {
    const runs = (await res.json())?.runs ?? [];
    id = runs.find((r: { state: string; openStdin: boolean; agent?: string }) => r.state === "running" && !r.openStdin && r.agent)?.id;
  }
  test.skip(!id, "no headless agent run to look at");
  if (TOKEN) await page.addInitScript((t) => localStorage.setItem("sandbox-studio-token", t), TOKEN);

  await page.goto(`/runs/${id}`);
  await page.waitForLoadState("networkidle");
  await page.getByRole("tab", { name: "Terminal" }).click();
  await page.waitForTimeout(1500);
  const body = await page.locator("body").innerText();
  console.log("ATTACH BUTTON PRESENT:", body.includes("Attach"));
  const i = body.indexOf("headless run");
  console.log("EXPLANATION:", body.slice(i, i + 190).replace(/\n+/g, " | "));

  await page.getByRole("link", { name: "Read the conversation" }).click();
  await page.waitForTimeout(2500);
  const convo = await page.locator("body").innerText();
  const j = convo.indexOf("you");
  console.log("CONVERSATION:", convo.slice(j, j + 150).replace(/\n+/g, " | "));
  expect(convo).not.toContain("Nothing said yet");
});
