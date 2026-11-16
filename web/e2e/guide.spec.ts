// Episode guide: every episode TMDB lists, with the ones you don't have
// greyed out and labelled, and seasons that aren't in the library.
import { expect, test } from "./fixtures";
import { press, snap, waitForFocus } from "./focus";
import { ids } from "./mock";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

test.afterEach(({ unhandled }) => {
  expect(unhandled, "API calls the mock doesn't answer (add them to e2e/mock.ts)").toEqual([]);
});

test("show page: counts, and a season you don't have", async ({ page, openTv }, info) => {
  await openTv(`/item/${ids.show}`);
  await waitForFocus(page, "play");
  await expect(page.locator(".tv-meta")).toContainText("5 seasons");
  await expect(page.locator(".tv-meta")).toContainText("28 of 38 episodes");
  await expect(page.locator(".tv-meta")).toContainText("Next episode 15 Jan 2099");
  const row = page.locator(".tv-row", { hasText: "Seasons" });
  await expect(row.locator(".tv-card")).toHaveCount(5);
  await expect(row.locator(".tv-card").first()).toContainText("10 of 12");
  const missing = row.locator(".tv-card.missing");
  await expect(missing).toHaveCount(1);
  await expect(missing).toContainText("Coming 1 Mar 2099");
  await snap(page, info, "show with guide");

  await press(page, "down");
  for (let i = 0; i < 4; i++) await press(page, "right");
  await press(page, "Enter");
  await expect(page).toHaveURL(new RegExp(`/item/${ids.show}/season/5(\\?|$)`));
  await expect(page.locator(".tv-detail-title")).toHaveText("Season 5");
  await expect(page.locator(".tv-row .tv-card.missing")).toHaveCount(8);
  await expect(page.locator(".tv-row .tv-card").first()).toContainText("Coming 1 Mar 2099");
  await expect(page.locator(".tv-row .tv-card").nth(1)).toContainText("Coming soon");
  await snap(page, info, "season not in library");
});

test("season page: missing and upcoming episodes in place, not playable", async ({ page, openTv }, info) => {
  await openTv(`/item/${ids.season}`);
  await waitForFocus(page, "play");
  const row = page.locator(".tv-row", { hasText: "Episodes" });
  await expect(row.locator(".tv-row-title")).toContainText("10 of 12");
  await expect(row.locator(".tv-card")).toHaveCount(12);
  await expect(row.locator(".tv-card.missing")).toHaveCount(2);
  await expect(row.locator(".tv-card").nth(10)).toContainText("Not in library");
  await expect(row.locator(".tv-card").nth(11)).toContainText("Coming 15 Jan 2099");
  await expect(page.locator(".tv-tab")).toHaveCount(5);
  await expect(page.locator(".tv-tab.missing")).toHaveText("Season 5");
  // Pressing a missing episode explains instead of playing.
  await row.locator(".tv-card").nth(11).focus();
  await press(page, "Enter");
  await expect(page.locator(".tv-toast, [role=status]").first()).toContainText("airs on 15 Jan 2099");
  await expect(page).toHaveURL(new RegExp(`/item/${ids.season}(\\?|$)`));
  await expect(page.locator(".tv-blurb")).toContainText("Coming 15 Jan 2099");
  await snap(page, info, "season with missing episodes");
});

test("web (phone) view: greyed seasons and episodes too", async ({ page }, info) => {
  await page.setViewportSize({ width: 430, height: 932 });
  await page.goto(`/item/${ids.show}?tv=0`);
  await expect(page.locator(".meta-line")).toContainText("5 seasons · 28 of 38 episodes");
  await expect(page.locator(".poster-card.missing")).toHaveCount(1);
  await expect(page.locator(".poster-card.missing")).toContainText("Coming 1 Mar 2099");
  await page.goto(`/item/${ids.season}?tv=0`);
  await expect(page.locator(".episode")).toHaveCount(12);
  await expect(page.locator(".episode.missing")).toHaveCount(2);
  await expect(page.locator(".episode.missing").last()).toContainText("Coming 15 Jan 2099");
  await page.locator(".episode.missing").first().scrollIntoViewIfNeeded();
  await snap(page, info, "web season");
  await page.locator(".season-tabs a.missing").click();
  await expect(page).toHaveURL(new RegExp(`/item/${ids.show}/season/5`));
  await expect(page.locator(".episode.missing")).toHaveCount(8);
  await snap(page, info, "web missing season");
});
