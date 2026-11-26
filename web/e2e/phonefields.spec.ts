// iPhone Safari zooms the page in, and leaves it zoomed, when a field whose
// text is smaller than 16px is tapped. On phones every field you type in or
// pick from is 16px; on a computer they keep their usual size.
import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import { ids, MOVIES_LIB } from "./mock";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

/** Font sizes (px) of every field on screen that opens the keyboard or a picker. */
const fieldSizes = (page: Page) =>
  page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>("input, select, textarea")]
      .filter((e) => !["range", "checkbox", "radio"].includes((e as HTMLInputElement).type))
      .map((e) => parseFloat(getComputedStyle(e).fontSize)),
  );

test("phone: search, sort, filters and Fix match fields are at least 16px", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`/library/${MOVIES_LIB}?tv=0`);
  await expect(page.locator(".poster-card").first()).toBeVisible();
  let sizes = await fieldSizes(page);
  expect(sizes.length).toBeGreaterThanOrEqual(2); // search box and the sort picker
  expect(Math.min(...sizes)).toBeGreaterThanOrEqual(16);

  // The top bar still fits: nothing pushed off the side of the screen.
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const box = await page.locator(".search-box").boundingBox();
  expect(box!.x + box!.width).toBeLessThanOrEqual(390);
  await page.screenshot({ path: "test-results/phonefields-library.png" });
  // The profile PIN box is already bigger, and stays that way.
  const pin = await page.evaluate(() => {
    const d = document.createElement("div");
    d.className = "modal pf-pin";
    d.innerHTML = '<input inputmode="numeric">';
    document.body.append(d);
    const size = getComputedStyle(d.querySelector("input")!).fontSize;
    d.remove();
    return size;
  });
  expect(pin).toBe("22px");

  await page.getByRole("button", { name: /Filters/ }).click();
  await expect(page.locator(".pk-sheet, .sheet, [role=dialog]").first()).toBeVisible();
  sizes = await fieldSizes(page);
  expect(sizes.length).toBeGreaterThan(2);
  expect(Math.min(...sizes)).toBeGreaterThanOrEqual(16);
  await page.screenshot({ path: "test-results/phonefields-filters.png" });

  await page.goto(`/item/${ids.movie}?tv=0`);
  await page.getByTitle("Fix match").click();
  await expect(page.locator(".modal")).toBeVisible();
  sizes = await fieldSizes(page);
  expect(sizes.length).toBeGreaterThanOrEqual(3); // search box, title, year
  expect(Math.min(...sizes)).toBeGreaterThanOrEqual(16);
  // The dialog fits the screen, Close button included.
  const modal = (await page.locator(".modal").boundingBox())!;
  expect(modal.x).toBeGreaterThanOrEqual(0);
  expect(modal.x + modal.width).toBeLessThanOrEqual(390);
  await page.screenshot({ path: "test-results/phonefields-fixmatch.png" });
});

test("computer: fields keep their usual size", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto(`/library/${MOVIES_LIB}?tv=0`);
  await expect(page.locator(".poster-card").first()).toBeVisible();
  expect(await fieldSizes(page)).toEqual([15, 15]);
});
