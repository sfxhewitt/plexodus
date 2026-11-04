// Long-press (hold OK) and the Menu key: the action menu on cards and library tabs.
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

const menuItems = (page: Page) => page.locator(".tv-menu .tv-menu-item");
const cwFirst = `c${ids.movie + 3}`; // the film in progress, first on Continue Watching

test("hold OK on Continue Watching: menu, Back, then remove", async ({ page, openTv }, info) => {
  await openTv("/");
  await waitForFocus(page, "hero-play");
  expect((await press(page, "down")).fk).toBe(cwFirst);

  await hold(page);
  await expect(page.locator(".tv-menu")).toBeVisible();
  await expect(menuItems(page)).toHaveText([
    "Resume", "Play from the start", "Remove from Continue Watching", "Add to Watchlist", "Mark as watched", "Details",
  ]);
  expect((await focused(page)).fk, "focus starts on the first action").toBe("menu-play");
  expect(page.url(), "holding OK must not also open the title").not.toContain("/item/");
  await snap(page, info, "01 menu");

  // Up/down stay inside the menu; Back closes it and focus returns to the card.
  expect((await press(page, "up")).fk).toBe("menu-play");
  await press(page, "down");
  expect((await press(page, "left")).fk, "Left doesn't leave the menu").toBe("menu-restart");
  await press(page, "Escape");
  await expect(page.locator(".tv-menu")).toHaveCount(0);
  expect((await focused(page)).fk).toBe(cwFirst);

  // Remove it: it leaves the row and focus moves to its neighbour, not the top of the page.
  await hold(page);
  await page.locator('[data-fk="menu-hide-continue"]').waitFor();
  await press(page, "down");
  expect((await press(page, "down")).fk).toBe("menu-hide-continue");
  await press(page, "Enter");
  await expect(page.locator(".tv-toast")).toHaveText("Removed from Continue Watching");
  const cwRow = page.locator(".tv-row", { has: page.locator(".tv-row-title", { hasText: "Continue Watching" }) });
  await expect(cwRow.locator(`[data-fk="${cwFirst}"]`)).toHaveCount(0);
  await expect.poll(async () => (await focused(page)).fk).toMatch(/^c\d+$/);
  const f = await focused(page);
  expect(f.ok && !f.inNav, "focus stays in the rows").toBe(true);
  await snap(page, info, "02 removed");
});

test("Menu key: add to watchlist, and a Watchlist row appears", async ({ page, openTv }) => {
  await openTv("/");
  await waitForFocus(page, "hero-play");
  await press(page, "down");
  await press(page, "down");
  await press(page, "down"); // Recently Added Movies
  const f = await focused(page);
  await press(page, "ContextMenu");
  await expect(page.locator(".tv-menu")).toBeVisible();
  await page.locator('[data-fk="menu-watchlist"]').focus();
  await press(page, "Enter");
  await expect(page.locator(".tv-toast")).toHaveText("Added to your Watchlist");
  await expect(page.locator(".tv-row-title", { hasText: "Watchlist" })).toHaveCount(1);
  expect((await focused(page)).fk, "focus stays on the card").toBe(f.fk);
});

test("a quick OK still opens the title", async ({ page, openTv }) => {
  await openTv("/");
  await waitForFocus(page, "hero-play");
  await press(page, "down");
  await press(page, "down"); // On Deck (opens the title rather than playing)
  const f = await focused(page);
  await press(page, "Enter");
  await expect(page).toHaveURL(new RegExp(`/item/${f.fk.slice(1)}`));
});

test("hold OK on a library tab: hide it from the menu", async ({ page, openTv }) => {
  await openTv("/");
  await waitForFocus(page, "hero-play");
  await press(page, "up");
  await press(page, "right");
  expect((await press(page, "right")).fk).toBe("nav-lib2");
  await hold(page);
  await expect(menuItems(page)).toHaveText(["Hide from the menu", "Hide from Home"]);
  await press(page, "Enter");
  await expect(page.locator('[data-fk="nav-lib2"]')).toHaveCount(0);
  const f = await focused(page);
  expect(f.ok && f.inNav, "focus stays in the tab bar").toBe(true);
});
