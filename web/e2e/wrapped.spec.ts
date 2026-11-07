// Smart rows on Home and the Plexodus Wrapped story, with the remote.
import { expect, test } from "./fixtures";
import { auditFocus, expectClean, focused, press, snap, waitForFocus } from "./focus";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

test.afterEach(({ unhandled }) => {
  expect(unhandled, "API calls the mock doesn't answer (add them to e2e/mock.ts)").toEqual([]);
});

test("home with smart rows and the Wrapped tile: focus audit", async ({ page, openTv }, info) => {
  await openTv("/?extras=1");
  await snap(page, info, "home extras");
  expectClean(await auditFocus(page), info, "home extras");
});

test("smart rows show their notes, and Wrapped opens from Home", async ({ page, openTv }, info) => {
  await openTv("/?extras=1");
  await waitForFocus(page, "hero-play");
  await press(page, "down"); // Up Next
  const tile = await press(page, "down");
  expect(tile.fk, "the Wrapped tile sits under the first row").toBe("wrapped");
  await snap(page, info, "01 wrapped tile");
  const season = await press(page, "down");
  expect(season.text, "Finish a Season card shows what's left").toContain("8 left");
  await expect(page.locator(".tv-row-eyebrow").first()).toHaveText("Tonight");
  await snap(page, info, "02 finish a season");

  await press(page, "up");
  await page.keyboard.press("Enter");
  await page.keyboard.up("Enter");
  await waitForFocus(page, "wr-next");
  await expect(page.locator(".wr-slide")).toContainText("2026");
  await snap(page, info, "03 wrapped intro");

  // Right through every slide to the summary, where the buttons take focus.
  const seen: string[] = [];
  for (let i = 0; i < 12; i++) {
    const f = await focused(page);
    if (f.fk !== "wr-next") break;
    seen.push((await page.locator(".wr-slide").innerText()).split("\n")[0]);
    await press(page, "right");
    await page.waitForTimeout(150);
    await snap(page, info, `04 slide ${i + 1}`);
  }
  expect(seen.length, "slides before the summary").toBeGreaterThanOrEqual(7);
  const f = await waitForFocus(page, "wr-again");
  expect(f.fk).toBe("wr-again");
  await expect(page.locator(".wr-slide")).toContainText("That was 2026");
  expect((await press(page, "right")).fk, "Right moves along the summary's buttons").toBe("wr-year-2025");

  // Another year, then Left from the first button steps back a slide.
  await page.keyboard.press("Enter");
  await page.keyboard.up("Enter");
  await waitForFocus(page, "wr-next");
  await expect(page.locator(".wr-slide")).toContainText("2025");
  await press(page, "right");
  await press(page, "left");
  expect((await focused(page)).fk, "Left goes back a slide, focus stays on the story").toBe("wr-next");

  // Back leaves Wrapped for Home.
  await page.keyboard.press("Escape");
  await waitForFocus(page);
  await expect(page.locator(".tv-wrapped")).toHaveCount(0);
});
