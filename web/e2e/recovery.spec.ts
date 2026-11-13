// Home and the library tabs recover when the server is slow or fails,
// instead of spinning forever.
import { expect, test } from "./fixtures";
import { focused, waitForFocus } from "./focus";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

test.afterEach(({ unhandled }) => {
  expect(unhandled, "API calls the mock doesn't answer (add them to e2e/mock.ts)").toEqual([]);
});

test("home that fails to load offers Try again, and the library tabs come back by themselves", async ({ page, openTv }) => {
  // Fail until the test says otherwise (dev-mode React runs effects twice).
  let failing = true;
  await page.route("**/api/home", (route) =>
    failing ? route.fulfill({ status: 500, contentType: "application/json", body: '{"error":"database is busy"}' }) : route.fallback());
  await page.route("**/api/libraries", (route) =>
    failing ? route.fulfill({ status: 500, contentType: "application/json", body: '{"error":"database is busy"}' }) : route.fallback());

  await openTv("/");
  await expect(page.getByText("Home didn't load")).toBeVisible();
  await expect(page.getByText("database is busy")).toBeVisible();
  expect((await focused(page)).text).toContain("Try again");
  await expect(page.locator(".tv-tab-btn")).toHaveCount(4); // search, Home, Watchlist, Collections only, for now

  failing = false;
  await page.keyboard.press("Enter");
  await waitForFocus(page, "hero-play");
  // The tab bar retries the library list on its own.
  await expect(page.locator(".tv-tab-btn")).not.toHaveCount(4, { timeout: 15_000 });
});
