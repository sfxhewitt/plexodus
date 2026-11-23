// Finishing something in the app's player and pressing Back: it leaves Up
// Next straight away, even while the server's Home still lists it, and a
// finished episode's card becomes the next episode.
import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import { waitForFocus } from "./focus";
import { ids } from "./mock";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

const tvBox = () => {
  const w = window as unknown as Record<string, unknown>;
  w.PlexodusNative = {
    isTv: () => true, version: () => "test", platform: () => "android", canEmbed: () => true,
    changeServer() {}, exit() {}, features: () => "recap",
    play(json: string) {
      w.__played = JSON.parse(json);
    },
  };
};

const inUpNext = (page: Page, id: number) => page.locator(`[data-group="home-Up Next"] [data-fk="c${id}"]`);

async function playFromUpNext(page: Page, id: number) {
  const card = inUpNext(page, id);
  await card.focus();
  await page.keyboard.press("Enter");
  await expect.poll(() => page.evaluate(() => (window as unknown as { __played?: { itemId: number } }).__played?.itemId)).toBe(id);
}

const closePlayer = (page: Page, d: { itemId: number; lastItemId?: number; position: number; duration: number; ended: boolean }) =>
  page.evaluate((detail) => window.dispatchEvent(new CustomEvent("plexodus:native-ended", { detail })), d);

test("a film finished in the player leaves Up Next at once", async ({ page, openTv }) => {
  await page.addInitScript(tvBox);
  await openTv("/");
  await waitForFocus(page);
  const film = ids.movie + 3;
  await expect(inUpNext(page, film)).toHaveCount(1);
  await playFromUpNext(page, film);
  // Back during the closing minutes: not the very end, but past 92%.
  await closePlayer(page, { itemId: film, lastItemId: film, position: 5100, duration: 5400, ended: false });
  await expect(inUpNext(page, film), "gone without waiting for the server").toHaveCount(0, { timeout: 500 });
  // The mock server still lists it; refetches don't bring it back.
  await page.waitForTimeout(3000);
  await expect(inUpNext(page, film)).toHaveCount(0);
  await expect(inUpNext(page, ids.episode), "the rest of Up Next stays").toHaveCount(1);
});

test("stopping partway leaves the film on Up Next", async ({ page, openTv }) => {
  await page.addInitScript(tvBox);
  await openTv("/");
  await waitForFocus(page);
  const film = ids.movie + 3;
  await playFromUpNext(page, film);
  await closePlayer(page, { itemId: film, lastItemId: film, position: 3000, duration: 5400, ended: false });
  await page.waitForTimeout(800);
  await expect(inUpNext(page, film)).toHaveCount(1);
});

test("a finished episode's card turns into the next episode", async ({ page, openTv }) => {
  await page.addInitScript(tvBox);
  // The show's next episode, once the server has the finished one saved.
  let next: unknown;
  await openTv("/");
  await waitForFocus(page);
  const show = await page.evaluate((id) => fetch(`/api/items/${id}`).then((r) => r.json()), ids.show);
  await page.route(`**/api/items/${ids.show}`, (route) =>
    next ? route.fulfill({ json: { ...show, onDeck: next } }) : route.fallback());
  await expect(inUpNext(page, ids.episode)).toHaveCount(1);
  await playFromUpNext(page, ids.episode);
  const nextId = ids.episode + 1;
  next = { id: nextId, libraryId: 2, kind: "episode", title: "Chapter 4", seasonNumber: 1, episodeNumber: 4, showId: ids.show, addedAt: 0 };
  await closePlayer(page, { itemId: ids.episode, lastItemId: ids.episode, position: 5000, duration: 5400, ended: false });
  await expect(inUpNext(page, ids.episode)).toHaveCount(0, { timeout: 500 });
  await expect(inUpNext(page, nextId), "the next episode takes its place").toHaveCount(1);
});

test("Remove from Up Next sticks when the app reopens before the server catches up", async ({ page, openTv }) => {
  await page.addInitScript(tvBox);
  await openTv("/");
  await waitForFocus(page);
  const film = ids.movie + 3;
  const stale = await page.evaluate(() => fetch("/api/home").then((r) => r.json()));
  await inUpNext(page, film).focus();
  await page.keyboard.down("Enter");
  await page.waitForTimeout(900);
  await page.keyboard.up("Enter");
  await page.locator(".tv-menu .tv-menu-item", { hasText: "Remove from Up Next" }).click();
  await expect(inUpNext(page, film)).toHaveCount(0);
  // The app reopens (Android closed it) and the server hands back its old Home.
  let calls = 0;
  await page.route("**/api/home", (route) => {
    calls++;
    return calls === 1
      ? route.fulfill({ json: stale, headers: { "X-Home-Stale": "1" } })
      : route.fallback();
  });
  await page.reload();
  await waitForFocus(page);
  await expect(inUpNext(page, ids.episode)).toHaveCount(1);
  await expect(inUpNext(page, film), "still removed").toHaveCount(0);
  // Told it was the old Home, the page asks again.
  await expect.poll(() => calls, { timeout: 6000 }).toBeGreaterThan(1);
});

test("an episode the server counts as watched (into its credits) leaves Up Next for the next one", async ({ page, openTv }) => {
  await page.addInitScript(tvBox);
  await openTv("/");
  await waitForFocus(page);
  const ep = await page.evaluate((id) => fetch(`/api/items/${id}`).then((r) => r.json()), ids.episode);
  const show = await page.evaluate((id) => fetch(`/api/items/${id}`).then((r) => r.json()), ids.show);
  await playFromUpNext(page, ids.episode);
  // Back at 88%: short of 90%, but past the credits marker the server knows.
  const nextId = ids.episode + 1;
  await page.route(`**/api/items/${ids.episode}`, (route) => route.fulfill({ json: { ...ep, watched: true, position: 0 } }));
  await page.route(`**/api/items/${ids.show}`, (route) =>
    route.fulfill({ json: { ...show, onDeck: { id: nextId, libraryId: 2, kind: "episode", title: "Chapter 4", seasonNumber: 1, episodeNumber: 4, showId: ids.show, addedAt: 0 } } }));
  await closePlayer(page, { itemId: ids.episode, lastItemId: ids.episode, position: 2376, duration: 2700, ended: false });
  await expect(inUpNext(page, ids.episode), "gone once the server says it's watched").toHaveCount(0, { timeout: 3000 });
  await expect(inUpNext(page, nextId), "the next episode takes its place").toHaveCount(1);
});
