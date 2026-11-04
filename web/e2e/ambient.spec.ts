// The TV extras: trailers that fade in behind the art, and the screensaver.
import { expect, test } from "./fixtures";
import { focused, press, snap, waitForFocus } from "./focus";
import { ids } from "./mock";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

test.afterEach(({ unhandled }) => {
  expect(unhandled, "API calls the mock doesn't answer (add them to e2e/mock.ts)").toEqual([]);
});

test("trailer fades in behind a film's page without taking the remote", async ({ page, openTv }, info) => {
  await openTv(`/item/${ids.movie}`);
  await waitForFocus(page, "play");
  const trailer = page.locator(".tv-backdrop .tv-trailer");
  await expect(trailer, "the trailer waits until you've rested on the page").toHaveCount(0);
  await expect(page.locator(".tv-backdrop .tv-trailer.on"), "then it fades in").toHaveCount(1, { timeout: 8000 });
  await page.waitForTimeout(1000);
  await snap(page, info, "01 trailer behind film");

  // Focus never goes into the YouTube frame: the D-pad still drives the page.
  const f = await focused(page);
  expect(f.ok, "focus stays on the page's buttons").toBe(true);
  expect((await press(page, "right")).fk).not.toBe("play");
  expect((await press(page, "left")).fk).toBe("play");

  // Leaving the page stops it.
  await page.keyboard.press("Escape");
  await expect(trailer).toHaveCount(0);
});

test("home: resting on the featured film plays its trailer in the hero", async ({ page, openTv }, info) => {
  await openTv("/");
  await waitForFocus(page, "hero-play");
  await expect(page.locator(".tv-hero.trailing .tv-trailer.on")).toHaveCount(1, { timeout: 8000 });
  await page.waitForTimeout(1000);
  await snap(page, info, "01 hero trailer");
  expect((await press(page, "right")).fk, "the D-pad still works over the trailer").toBe("hero-info");
  // Moving down into the rows stops it.
  await press(page, "down");
  await expect(page.locator(".tv-hero .tv-trailer")).toHaveCount(0);
});

test("titles without a trailer just keep their art", async ({ page, openTv }) => {
  await openTv(`/item/${ids.show}`);
  await waitForFocus(page, "play");
  await page.waitForTimeout(4000);
  await expect(page.locator(".tv-trailer")).toHaveCount(0);
});

test("trailers can be switched off in Settings", async ({ page, openTv }) => {
  await openTv("/settings");
  await waitForFocus(page);
  await page.locator('[data-fk="trailers"]').focus();
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-fk="trailers"]')).not.toHaveClass(/\bon\b/);
  await openTv(`/item/${ids.movie}`);
  await waitForFocus(page, "play");
  await page.waitForTimeout(4000);
  await expect(page.locator(".tv-trailer")).toHaveCount(0);
});

test("screensaver: drifts through art, any button wakes, OK plays", async ({ page, openTv }, info) => {
  // Inside the Android app, so OK hands the title to its player.
  await page.addInitScript(() => {
    const w = window as unknown as Record<string, unknown>;
    w.PlexodusNative = {
      isTv: () => true, version: () => "test", platform: () => "android", canEmbed: () => true,
      changeServer() {}, exit() {},
      play(json: string) {
        w.__played = JSON.parse(json);
      },
    };
  });
  await openTv("/settings");
  await waitForFocus(page);
  const preview = page.locator('[data-fk="saver-preview"]');
  await preview.focus();
  await page.keyboard.press("Enter");
  const saver = page.locator(".tv-saver");
  await expect(saver).toHaveCount(1);
  await expect(page.locator(".tv-saver-info"), "shows the title and a play hint").toContainText("to play");
  await page.waitForTimeout(1500);
  await snap(page, info, "01 screensaver");

  // An arrow only wakes it: focus doesn't move underneath.
  await page.keyboard.press("ArrowRight");
  await expect(saver).toHaveCount(0);
  expect((await focused(page)).fk, "focus is where it was").toBe("saver-preview");

  // Back wakes it without leaving Settings.
  await page.keyboard.press("Enter");
  await expect(saver).toHaveCount(1);
  await page.keyboard.press("Escape");
  await expect(saver).toHaveCount(0);
  expect(new URL(page.url()).pathname).toBe("/settings");

  // OK plays what's on screen.
  await page.keyboard.press("Enter");
  await expect(page.locator(".tv-saver-info")).toHaveCount(1);
  const shown = await page.locator(".tv-saver-info").innerText();
  await page.keyboard.press("Enter");
  await expect(saver).toHaveCount(0);
  await expect.poll(() => page.evaluate(() => (window as unknown as { __played?: { title: string } }).__played?.title),
    { message: "OK hands the title on screen to the player" }).toBeTruthy();
  const played = await page.evaluate(() => (window as unknown as { __played: { title: string } }).__played.title);
  expect(shown, "it plays the title that was on screen").toContain(played);
  expect(new URL(page.url()).pathname, "and stays on Settings").toBe("/settings");
});

test("screensaver starts by itself after the idle time", async ({ page, openTv }) => {
  await page.addInitScript(() => {
    localStorage.setItem("plexodus.tv.ambient", JSON.stringify({ screensaver: 0.02 }));
  });
  await openTv("/");
  await waitForFocus(page, "hero-play");
  await expect(page.locator(".tv-saver")).toHaveCount(1, { timeout: 8000 });
  await page.keyboard.press("ArrowDown");
  await expect(page.locator(".tv-saver")).toHaveCount(0);
  expect((await focused(page)).fk, "the waking press doesn't move focus").toBe("hero-play");
});
