// The automation each sandbox runs. Edit `visit` for your own task; the rest
// is plumbing. It drives the image's own Chromium (/usr/bin/chromium) through
// playwright-core, so no browser is downloaded and no root is needed.
//
// Input:  ./urls.json          — this worker's share of the URLs
// Output: ./out/results.json   — one record per URL
//         ./out/NN.png         — a screenshot per URL
// Env:    WORKER    — this worker's index (for logs)
//         HEADED=1  — draw on the desktop (DISPLAY=:1) so Studio's Desktop tab shows it
//         HOLD      — seconds to keep the browser open at the end, to look at it
//         SLOWMO    — milliseconds added to every browser action, to follow it
const fs = require("fs");
const path = require("path");
const { chromium } = require("playwright-core");

const worker = process.env.WORKER || "0";
const headed = process.env.HEADED === "1";
const hold = Number(process.env.HOLD || 0);
const outDir = path.join(__dirname, "out");
const log = (...a) => console.error(`[worker ${worker}]`, ...a);

// One URL. Return anything JSON-serialisable; it lands in results.json.
async function visit(page, url, i) {
  const res = await page.goto(url, { waitUntil: "domcontentloaded", timeout: 30_000 });
  await page.waitForLoadState("networkidle", { timeout: 10_000 }).catch(() => {});
  const shot = `${String(i).padStart(2, "0")}.png`;
  await page.screenshot({ path: path.join(outDir, shot), fullPage: false });
  return {
    status: res ? res.status() : null,
    title: await page.title(),
    h1: await page.locator("h1").first().textContent({ timeout: 2_000 }).catch(() => null),
    links: await page.locator("a[href]").count(),
    screenshot: shot,
  };
}

(async () => {
  fs.mkdirSync(outDir, { recursive: true });
  const urls = JSON.parse(fs.readFileSync(path.join(__dirname, "urls.json"), "utf8"));
  const browser = await chromium.launch({
    executablePath: "/usr/bin/chromium",
    headless: !headed,
    env: headed ? { ...process.env, DISPLAY: ":1" } : process.env,
    // The VM is the boundary; small guests have a small /dev/shm.
    args: ["--disable-dev-shm-usage"],
    slowMo: Number(process.env.SLOWMO || 0),
  });
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  const results = [];
  for (const [i, url] of urls.entries()) {
    const page = await context.newPage();
    const started = Date.now();
    try {
      results.push({ url, ok: true, ms: 0, ...(await visit(page, url, i)) });
    } catch (e) {
      results.push({ url, ok: false, error: String(e.message || e).split("\n")[0] });
    }
    results[results.length - 1].ms = Date.now() - started;
    log(results[results.length - 1].ok ? "ok  " : "FAIL", url);
    // Headed, the pages stay open as tabs, to look at with HOLD.
    if (!headed) await page.close();
  }
  if (hold > 0) {
    log(`holding the browser open for ${hold}s`);
    await new Promise((r) => setTimeout(r, hold * 1000));
  }
  await browser.close();
  fs.writeFileSync(path.join(outDir, "results.json"), JSON.stringify(results, null, 2));
  process.exit(results.every((r) => r.ok) ? 0 : 1);
})().catch((e) => {
  log("fatal:", e);
  process.exit(2);
});
