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

test("the title page says Watched as soon as the player finishes the film", async ({ page, openTv }) => {
  await page.addInitScript(tvBox);
  const film = ids.movie + 3;
  await openTv(`/item/${film}`);
  await waitForFocus(page);
  const watchedBtn = page.locator('[data-fk="watched"]');
  await expect(watchedBtn).toHaveText(/Mark watched/);
  await page.locator('[data-fk="play"]').focus();
  await page.keyboard.press("Enter");
  await expect.poll(() => page.evaluate(() => (window as unknown as { __played?: { itemId: number } }).__played?.itemId)).toBe(film);
  // The mock server never saves the progress: the page must not wait for it.
  await closePlayer(page, { itemId: film, lastItemId: film, position: 5100, duration: 5400, ended: false });
  await expect(watchedBtn, "flips without waiting for the server").toHaveText(/Mark unwatched/, { timeout: 500 });
  await expect(page.locator(".tv-watched")).toHaveCount(1);
  await expect(page.locator('[data-fk="play"]')).toHaveText(/^\s*Play/);
  // Refetches of the server's old copy don't flip it back.
  await page.waitForTimeout(3000);
  await expect(watchedBtn).toHaveText(/Mark unwatched/);
});

test("stopping partway leaves the title page unwatched", async ({ page, openTv }) => {
  await page.addInitScript(tvBox);
  const film = ids.movie + 3;
  await openTv(`/item/${film}`);
  await waitForFocus(page);
  await page.locator('[data-fk="play"]').focus();
  await page.keyboard.press("Enter");
  await expect.poll(() => page.evaluate(() => (window as unknown as { __played?: { itemId: number } }).__played?.itemId)).toBe(film);
  await closePlayer(page, { itemId: film, lastItemId: film, position: 3000, duration: 5400, ended: false });
  await page.waitForTimeout(800);
  await expect(page.locator('[data-fk="watched"]')).toHaveText(/Mark watched/);
});

test("the page saves a finished film itself when the player's reports never arrived", async ({ page, openTv }) => {
  await page.addInitScript(tvBox);
  const film = ids.movie + 3;
  const saves: { position: number; duration: number }[] = [];
  const logs: { fixed?: string; stats?: { failed: number } }[] = [];
  await page.route(`**/api/items/${film}/progress`, (route) => {
    saves.push(route.request().postDataJSON());
    return route.fulfill({ json: { watched: true } });
  });
  await page.route("**/api/player/closed", (route) => {
    logs.push(route.request().postDataJSON());
    return route.fulfill({ json: { ok: true } });
  });
  await openTv(`/item/${film}`);
  await waitForFocus(page);
  await page.locator('[data-fk="play"]').focus();
  await page.keyboard.press("Enter");
  await expect.poll(() => page.evaluate(() => (window as unknown as { __played?: { itemId: number } }).__played?.itemId)).toBe(film);
  // The server still has the film unwatched after the player closed at the end.
  await page.evaluate((detail) => window.dispatchEvent(new CustomEvent("plexodus:native-ended", { detail })),
    { itemId: film, lastItemId: film, position: 5390, duration: 5400, ended: false, stats: { ok: 0, failed: 12 } });
  await expect.poll(() => saves.length, { timeout: 8000 }).toBe(1);
  expect(saves[0]).toMatchObject({ position: 5390, duration: 5400 });
  await expect.poll(() => logs.length).toBe(1);
  expect(logs[0].fixed).toBe("marked it watched");
  expect(logs[0].stats?.failed).toBe(12);
});

test("the title page catches up when the app never says the player closed", async ({ page, openTv }) => {
  await page.addInitScript(tvBox);
  const film = ids.movie + 3;
  await openTv(`/item/${film}`);
  await waitForFocus(page);
  const item = await page.evaluate((id) => fetch(`/api/items/${id}`).then((r) => r.json()), film);
  let watched = false;
  await page.route(`**/api/items/${film}`, (route) =>
    watched ? route.fulfill({ json: { ...item, watched: true, position: 0 } }) : route.fallback());
  const logs: { missed?: boolean }[] = [];
  await page.route("**/api/player/closed", (route) => {
    logs.push(route.request().postDataJSON());
    return route.fulfill({ json: { ok: true } });
  });
  const watchedBtn = page.locator('[data-fk="watched"]');
  await expect(watchedBtn).toHaveText(/Mark watched/);
  await page.locator('[data-fk="play"]').focus();
  await page.keyboard.press("Enter");
  await expect.poll(() => page.evaluate(() => (window as unknown as { __played?: { itemId: number } }).__played?.itemId)).toBe(film);
  // The film is watched to the end; the server has it, but the app's
  // "player closed" message never reaches the page.
  watched = true;
  await page.waitForTimeout(3200);
  await expect(watchedBtn, "nothing yet: the page can't tell").toHaveText(/Mark watched/);
  // Back on the page, the first key press is enough.
  await page.keyboard.press("ArrowRight");
  await expect(watchedBtn).toHaveText(/Mark unwatched/, { timeout: 4000 });
  await expect.poll(() => logs.length).toBe(1);
  expect(logs[0].missed).toBe(true);
});
