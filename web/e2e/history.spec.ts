// History: everything played, newest first by day, reached from the tab bar.
import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import { focused, press, snap, waitForFocus } from "./focus";
import { ids } from "./mock";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

test.afterEach(({ unhandled }) => {
  expect(unhandled, "API calls the mock doesn't answer (add them to e2e/mock.ts)").toEqual([]);
});

async function hold(page: Page, ms = 800) {
  await page.keyboard.down("Enter");
  await page.waitForTimeout(ms);
  await page.keyboard.up("Enter");
}

test("History tab: days newest first, resume points, remove one", async ({ page, openTv }, info) => {
  await openTv("/");
  await waitForFocus(page, "hero-play");
  await press(page, "up");
  await press(page, "right");
  expect((await press(page, "right")).fk).toBe("nav-history");
  await press(page, "Enter");
  await expect(page).toHaveURL(/\/history$/);

  // Today, Yesterday, then the weekday.
  await expect(page.locator(".tv-row-title")).toHaveCount(3);
  await expect(page.locator(".tv-row-title").nth(0)).toHaveText("Today");
  await expect(page.locator(".tv-row-title").nth(1)).toHaveText("Yesterday");
  const first = `c${ids.movie + 3}`;
  await waitForFocus(page, first);
  // The short play of an episode is there, with how far in.
  const today = page.locator(".tv-row").first();
  await expect(today.locator(".tv-card")).toHaveCount(2);
  await expect(today.locator(".tv-card").nth(1)).toContainText("1m of 45m");
  await expect(page.locator(".tv-row").nth(1)).toContainText("Watched");
  await snap(page, info, "01 history");

  // Down moves through the days.
  expect((await press(page, "down")).fk).toBe(`c${ids.movie}`);
  await press(page, "up");
  expect((await focused(page)).fk).toBe(first);

  // Hold OK: Resume, and Remove from History.
  await hold(page);
  const items = page.locator(".tv-menu .tv-menu-item");
  await expect(items.first()).toHaveText("Resume");
  await expect(items.filter({ hasText: "Remove from History" })).toHaveCount(1);
  await page.locator('[data-fk="menu-hide-history"]').focus();
  await press(page, "Enter");
  await expect(page.locator(".tv-toast")).toHaveText("Removed from History");
  await expect(page.locator(`[data-fk="${first}"]`)).toHaveCount(0);
  await expect(page.locator(".tv-row").first().locator(".tv-card")).toHaveCount(1);
});

test("History on the phone/web view: Resume links and remove", async ({ page }, info) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/history?tv=0");
  await expect(page.locator(".history-day h2")).toHaveCount(3);
  await expect(page.locator(".history-row")).toHaveCount(4);
  const first = page.locator(".history-row").first();
  await expect(first.locator("a.btn")).toHaveAttribute("href", `/play/${ids.movie + 3}`);
  // A finished film plays again from the start.
  await expect(page.locator(".history-day").nth(1).locator("a.btn")).toHaveAttribute("href", `/play/${ids.movie}?from=0`);
  // Phones get the sections in the bottom tab bar.
  await expect(page.locator('.tabbar a[href="/history"]')).toBeVisible();
  await expect(page.locator('.tabbar a[href="/history"]')).toHaveClass(/active/);
  await snap(page, info, "02 history phone");
  await first.getByRole("button", { name: /Remove .* from History/ }).click();
  await expect(page.locator(".history-row")).toHaveCount(3);
});
