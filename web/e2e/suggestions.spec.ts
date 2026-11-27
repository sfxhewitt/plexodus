// "Remove from suggestions": hold OK on a Home card on the TV, or press and
// hold it on a phone, and the title stops being suggested.
import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import { focused, press, waitForFocus } from "./focus";
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

const tvRow = (page: Page, title: string | RegExp) => page.locator(".tv-row", { has: page.locator(".tv-row-title", { hasText: title }) });

test("TV: hold OK on a Because You Watched card and remove it from suggestions", async ({ page, openTv }, info) => {
  await openTv("/?extras=1");
  await waitForFocus(page, "hero-play");
  const because = tvRow(page, /Because You Watched/);
  const first = because.locator("[data-fk^='c']").nth(1); // an unwatched one
  const fk = (await first.getAttribute("data-fk"))!;
  const id = Number(fk.slice(1));
  const second = await because.locator("[data-fk^='c']").nth(2).getAttribute("data-fk");

  const title = (await first.locator(".t").textContent())!;
  await first.focus();
  let homeFetches = 0;
  const posts: string[] = [];
  page.on("request", (r) => {
    if (r.url().includes("/api/home")) homeFetches++;
    if (r.method() === "POST") posts.push(new URL(r.url()).pathname);
  });
  await hold(page);
  await expect(page.locator(".tv-menu")).toBeVisible();
  await expect(page.locator(".tv-menu .tv-menu-item")).toHaveText([
    "Play", "Remove from suggestions", "Add to Watchlist", "Mark as watched", "Details",
  ]);
  await page.screenshot({ path: info.outputPath("tv-menu.png") });
  expect((await press(page, "down")).fk).toBe("menu-hide-suggest");
  await press(page, "Enter");

  // Gone from every row at once, with no reload of Home; focus moves along.
  await expect(page.locator(`.tv-home-rows [data-fk="${fk}"]`)).toHaveCount(0);
  await expect.poll(async () => (await focused(page)).fk, { message: "focus moves to the next card" }).toBe(second);
  await expect(page.locator(".tv-toast")).toHaveText("Removed. It won't be suggested again.");
  expect(posts).toContain(`/api/items/${id}/suggestions/hide`);
  expect(homeFetches, "removing doesn't reload Home").toBe(0);

  // Still gone after Home is loaded afresh (the server keeps it out).
  await page.reload();
  await waitForFocus(page);
  await expect(tvRow(page, /Because You Watched/)).toBeVisible();
  await expect(page.locator(`.tv-home-rows [data-fk="${fk}"]`)).toHaveCount(0);

  // Settings lists it and can put it back.
  await openTv("/settings/libraries");
  const sec = page.getByTestId("removed-suggestions");
  await expect(sec).toContainText(title);
  await page.screenshot({ path: info.outputPath("tv-settings.png") });
  await sec.locator(`[data-fk="removed-${id}"]`).focus();
  await press(page, "Enter");
  await expect(sec).toContainText("Nothing yet");
  await openTv("/?extras=1");
  await expect(tvRow(page, /Because You Watched/).locator(`[data-fk="${fk}"]`)).toHaveCount(1);
});

test("TV: Up Next cards keep Remove from Up Next, without Remove from suggestions", async ({ page, openTv }) => {
  await openTv("/");
  await waitForFocus(page, "hero-play");
  await press(page, "down");
  await hold(page);
  await expect(page.locator('[data-fk="menu-hide-continue"]')).toBeVisible();
  await expect(page.locator('[data-fk="menu-hide-suggest"]')).toHaveCount(0);
});

test.describe("phone", () => {
  test.use({ hasTouch: true, viewport: { width: 390, height: 844 } });

  test("press and hold a suggestion: remove it, undo, remove again, bring them all back", async ({ page }, info) => {
    await page.goto("/?tv=0&extras=1");
    const row = page.locator(".row", { has: page.locator(".row-title", { hasText: /Because You Watched/ }) });
    const card = row.locator(".poster-card").first();
    const href = (await card.getAttribute("href"))!;
    const everywhere = page.locator(`.rows a[href="${href}"]`);
    await expect(everywhere).not.toHaveCount(0);
    await card.scrollIntoViewIfNeeded();

    // A tap still opens the title; a held press opens the sheet and doesn't.
    const holdOn = async () => {
      const box = (await row.locator(".poster-card").first().boundingBox())!;
      const [x, y] = [box.x + box.width / 2, box.y + box.height / 2];
      await page.dispatchEvent(`.rows a[href="${href}"] >> nth=0`, "pointerdown", { pointerType: "touch", clientX: x, clientY: y, bubbles: true });
      await page.waitForTimeout(650);
      await page.dispatchEvent(`.rows a[href="${href}"] >> nth=0`, "pointerup", { pointerType: "touch", clientX: x, clientY: y, bubbles: true });
      await page.dispatchEvent(`.rows a[href="${href}"] >> nth=0`, "click", { bubbles: true, cancelable: true });
    };
    await holdOn();
    const sheet = page.getByTestId("card-sheet");
    await expect(sheet).toBeVisible();
    expect(new URL(page.url()).pathname, "holding doesn't open the title").toBe("/");
    await expect(sheet).toContainText("Remove from suggestions");
    await page.screenshot({ path: info.outputPath("phone-sheet.png") });

    await page.getByTestId("sheet-remove").click();
    await expect(sheet).toHaveCount(0);
    await expect(everywhere).toHaveCount(0);
    const note = page.getByTestId("home-note");
    await expect(note).toContainText("Removed from your suggestions");
    await page.screenshot({ path: info.outputPath("phone-removed.png") });

    // Undo puts it back.
    await note.getByRole("button", { name: "Undo" }).click();
    await expect(everywhere).not.toHaveCount(0);
    await expect(page.getByTestId("home-removed")).toHaveCount(0);

    // Remove again: it stays away after a reload, and Home offers to bring it back.
    await holdOn();
    await page.getByTestId("sheet-remove").click();
    await expect(everywhere).toHaveCount(0);
    await page.reload();
    await expect(page.locator(".row").first()).toBeVisible();
    await expect(everywhere).toHaveCount(0);
    const back = page.getByTestId("home-removed");
    await expect(back).toBeVisible();
    await back.getByRole("button", { name: "Bring them back" }).click();
    await expect(everywhere).not.toHaveCount(0);
    await expect(back).toHaveCount(0);
  });

  test("press and hold on Up Next offers Remove from Up Next", async ({ page }) => {
    await page.goto("/?tv=0");
    const card = page.locator(".row-landscape .land-card").first();
    await expect(card).toBeVisible();
    await card.dispatchEvent("contextmenu", { bubbles: true, cancelable: true });
    await expect(page.getByTestId("card-sheet")).toContainText("Remove from Up Next");
    const href = (await card.getAttribute("href"))!;
    await page.getByTestId("sheet-remove").click();
    await expect(page.locator(`.row-landscape a[href="${href}"]`)).toHaveCount(0);
    await expect(page.getByTestId("home-note")).toContainText("Removed from Up Next");
    expect(ids.movie).toBeGreaterThan(0);
  });
});
