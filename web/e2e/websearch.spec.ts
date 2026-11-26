// The web search box: a title's matches show as you type, Enter on a word
// or two leaves them on screen, and Enter on a longer request asks in plain
// English with any matching titles first.
import { expect, test } from "./fixtures";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

test("phone: Enter on a title keeps its matches; a request is asked in plain English", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const asked: string[] = [];
  page.on("request", (r) => { if (new URL(r.url()).pathname === "/api/ask") asked.push(r.url()); });
  await page.goto("/?tv=0");
  const box = page.getByPlaceholder("Search or ask…");
  const title = "Low Tide";
  await box.fill(title);
  await expect(page.locator(".search .grid a").first()).toBeVisible();
  await box.press("Enter");
  await page.waitForTimeout(400);
  expect(page.url()).not.toContain("ask=1");
  expect(asked).toEqual([]);
  await expect(page.locator(".search .grid a").first()).toBeVisible();

  await box.fill(`something like ${title}`);
  await box.press("Enter");
  await expect(page).toHaveURL(/ask=1/);
  await expect(page.getByRole("heading", { name: "Best fits" })).toBeVisible();
  // Only the finished request is asked, never the half-typed text.
  expect([...new Set(asked.map((u) => new URL(u).searchParams.get("q")))]).toEqual([`something like ${title}`]);
});

test("phone: a plain-English request lists matching titles above the answer", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/search?q=Low+Tide&ask=1&tv=0");
  await expect(page.getByRole("heading", { name: "Titles matching “Low Tide”" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Best fits" })).toBeVisible();
  const order = await page.locator(".search h2").allTextContents();
  expect(order.indexOf("Titles matching “Low Tide”")).toBeLessThan(order.indexOf("Best fits"));
});

test("phone: the keyboard shows Search, titles aren't corrected, and the x clears the box", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/?tv=0");
  const box = page.getByPlaceholder("Search or ask…");
  await expect(box).toHaveAttribute("enterkeyhint", "search");
  await expect(box).toHaveAttribute("autocapitalize", "none");
  await expect(box).toHaveAttribute("autocorrect", "off");
  await expect(box).toHaveAttribute("spellcheck", "false");
  const clear = page.getByRole("button", { name: "Clear search" });
  await expect(clear).toHaveCount(0);

  await box.fill("Low Tide");
  await expect(page.locator(".search .grid a").first()).toBeVisible();
  await expect(clear).toBeVisible();
  // Big enough to hit with a thumb, and inside the box.
  const b = (await clear.boundingBox())!;
  const s = (await page.locator(".search-box").boundingBox())!;
  expect(b.width).toBeGreaterThanOrEqual(32);
  expect(b.x + b.width).toBeLessThanOrEqual(s.x + s.width);
  await page.screenshot({ path: "test-results/websearch-clear.png" });

  await clear.click();
  await expect(box).toHaveValue("");
  await expect(box).toBeFocused();
  await expect(clear).toHaveCount(0);
  await expect(page).not.toHaveURL(/\/search/);
  // Ready to type the next one straight away.
  await page.keyboard.type("Ash");
  await expect(page).toHaveURL(/\/search\?q=Ash/);
});

test("computer: the / hint shows until something is typed", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/?tv=0");
  await expect(page.locator(".search-box kbd")).toBeVisible();
  await page.getByPlaceholder("Search or ask…").fill("Ash");
  await expect(page.locator(".search-box kbd")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Clear search" })).toBeVisible();
});
