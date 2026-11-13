// Watchlist in the tab bar, Collections pages, and a film's collection row.
import { expect, test } from "./fixtures";
import { press, snap, waitForFocus } from "./focus";
import { ids } from "./mock";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

test.afterEach(({ unhandled }) => {
  expect(unhandled, "API calls the mock doesn't answer (add them to e2e/mock.ts)").toEqual([]);
});

test("watchlist tab: empty, then lists what you add", async ({ page, openTv }) => {
  await openTv("/watchlist");
  await expect(page.locator(".tv-library")).toContainText("Nothing on your Watchlist yet");
  await openTv(`/item/${ids.movie}`);
  await waitForFocus(page, "play");
  await page.locator('[data-fk="watchlist"]').click();
  await expect(page.locator('[data-fk="watchlist"]')).toContainText("On your Watchlist");
  await openTv("/watchlist");
  await waitForFocus(page, `c${ids.movie}`);
  await expect(page.locator(".tv-count")).toContainText("1 watched");
});

test("collections: grid, a collection, and the row on a film", async ({ page, openTv }) => {
  await openTv("/collection");
  await waitForFocus(page, "col900");
  await expect(page.locator(".tv-h1")).toHaveText("Collections");
  await press(page, "Enter");
  await expect(page).toHaveURL(/\/collection\/900/);
  await expect(page.locator(".tv-detail-title")).toHaveText("The Atlas Collection");
  await expect(page.locator(".tv-grid [data-f]")).toHaveCount(3);

  await openTv(`/item/${ids.collected}`);
  await waitForFocus(page, "play");
  const row = page.locator('[data-row]', { hasText: "The Atlas Collection" });
  await expect(row.locator(".tv-card:not(.tv-collection-tile)")).toHaveCount(3);
  await expect(row.locator('[data-fk="col900"]')).toHaveCount(1);
});

test("a film's page: cast, then more like this", async ({ page, openTv }, info) => {
  await openTv(`/item/${ids.extras}`);
  await waitForFocus(page, "play");
  await expect(page.locator(".tv-person")).toHaveCount(2);
  await expect(page.locator(".tv-person-photo").nth(1)).toHaveText("TO");
  const row = page.locator(".tv-row", { hasText: "More like this" });
  await expect(row.locator(".tv-card")).toHaveCount(6);
  const f = await press(page, "down");
  expect(f.fk, "Down from the buttons skips the cast into More like this").toMatch(/^c\d+$/);
  await snap(page, info, "film extras");
});
