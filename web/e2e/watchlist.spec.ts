// The phone/web title page has an obvious Watchlist toggle, and the Watchlist
// itself is one tap away in the tab bar.
import { expect, test } from "./fixtures";
import { ids } from "./mock";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

test("phone title page: add to Watchlist, find it in the Watchlist tab, remove it", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`/item/${ids.movie}?tv=0`);
  const btn = page.getByTestId("watchlist-toggle");
  await expect(btn).toHaveText(/Add to Watchlist/);
  await btn.click();
  await expect(btn).toHaveText(/On your Watchlist/);
  await page.screenshot({ path: "test-results/watchlist-title-phone.png" });

  const tab = page.locator('.tabbar a[href="/watchlist"]');
  await expect(tab).toBeVisible();
  // Every tab's label fits without being cut off.
  for (const s of await page.locator(".tabbar a > span:not(.np-badge)").all()) {
    expect(await s.evaluate((e) => e.scrollWidth <= e.clientWidth)).toBe(true);
  }
  await tab.click();
  await expect(page.locator(".watchlist-page .poster-card")).toHaveCount(1);
  await page.screenshot({ path: "test-results/watchlist-page-phone.png" });

  await page.locator(".watchlist-page .poster-card").click();
  await page.getByTestId("watchlist-toggle").click();
  await expect(page.getByTestId("watchlist-toggle")).toHaveText(/Add to Watchlist/);
  await page.goto("/watchlist?tv=0");
  await expect(page.locator(".watchlist-page .page-msg")).toContainText("Nothing on your Watchlist yet");
});

test("shows get the Watchlist toggle too; seasons and episodes don't", async ({ page }) => {
  await page.goto(`/item/${ids.show}?tv=0`);
  await expect(page.getByTestId("watchlist-toggle")).toBeVisible();
  await expect(page.locator('.nav-links a[href="/watchlist"]')).toBeVisible();
});
