// Rotten Tomatoes scores: a small badge on posters, the full scores on title
// pages, and a TV setting that hides the poster badges.
import { expect, test } from "./fixtures";
import { snap, waitForFocus } from "./focus";
import { ids, MOVIES_LIB } from "./mock";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

test.afterEach(({ unhandled }) => {
  expect(unhandled, "API calls the mock doesn't answer (add them to e2e/mock.ts)").toEqual([]);
});

test("TV: poster badges, title page scores, and the setting to hide badges", async ({ page, openTv }, info) => {
  await openTv(`/library/${MOVIES_LIB}`);
  const badges = page.locator(".tv-card .rt-badge");
  await expect(badges.first()).toBeVisible();
  // Films without scores get no badge.
  expect(await badges.count()).toBeLessThan(await page.locator(".tv-card.poster").count());
  await expect(badges.first().locator(".rt-icon").first()).toBeVisible();
  await expect(badges.first()).toContainText("%");
  await snap(page, info, "01 library badges");

  // movies[0]: critics 41% (rotten splat), audience 40% (spilled popcorn).
  await openTv(`/item/${ids.movie}`);
  const scores = page.locator(".tv-detail-info .rt");
  await expect(scores).toContainText("41%");
  await expect(scores).toContainText("Critics");
  await expect(scores).toContainText("Audience");
  await expect(page.locator(".tv-detail-info .tv-rating")).toHaveCount(0);
  await snap(page, info, "02 title page");

  await openTv("/settings");
  await page.locator('[data-fk="rt-posters"]').focus();
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-fk="rt-posters"]')).toHaveAttribute("aria-pressed", "false");
  await openTv(`/library/${MOVIES_LIB}`);
  await waitForFocus(page);
  await expect(page.locator(".tv-card .rt-badge").first()).toBeHidden();
});

test("phone: poster badges and title page scores", async ({ page }, info) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`/library/${MOVIES_LIB}?tv=0`);
  const badges = page.locator(".poster-card .rt-badge");
  await expect(badges.first()).toBeVisible();
  await page.screenshot({ path: info.outputPath("03 phone library.png") });
  await page.goto(`/item/${ids.movie}?tv=0`);
  await expect(page.locator(".meta-line .rt")).toContainText("41%");
  await page.waitForTimeout(800);
  await page.screenshot({ path: info.outputPath("04 phone title.png") });
});
