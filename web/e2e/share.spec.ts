// Share: puts a film or show on a linked person's Plex Watchlist.
import { expect, test } from "./fixtures";
import { waitForFocus } from "./focus";
import { ids } from "./mock";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");
test.use({ share: true });

test.afterEach(({ unhandled }) => {
  expect(unhandled, "API calls the mock doesn't answer (add them to e2e/mock.ts)").toEqual([]);
});

test("a film's page: Share with Dad adds it to his Plex watchlist", async ({ page, openTv }) => {
  await openTv(`/item/${ids.movie}`);
  await waitForFocus(page, "play");
  const share = page.locator('[data-fk="share-555"]');
  await expect(share).toContainText("Share with Dad");
  await share.click();
  await expect(page.locator("body")).toContainText("to Dad's Plex watchlist");
});
