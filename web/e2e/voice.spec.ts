// "Play / continue <title>" from a voice assistant: the app opens /voice?q=…
// and the title is found and handed to the app's player.
import { expect, test } from "./fixtures";
import { parseVoice } from "../src/tv/voiceAsk";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

test.afterEach(({ unhandled }) => {
  expect(unhandled, "API calls the mock doesn't answer (add them to e2e/mock.ts)").toEqual([]);
});

type W = { __played?: { itemId: number; title: string; subtitle: string; startSeconds: number } };

const app = () => {
  const w = window as unknown as Record<string, unknown> & W;
  w.PlexodusNative = {
    isTv: () => true, version: () => "test", platform: () => "android", canEmbed: () => true,
    changeServer() {}, exit() {}, features: () => "recap,avsync",
    play(json: string) { w.__played = JSON.parse(json); },
  };
};

test("parses what was said", () => {
  expect(parseVoice("mobland")).toEqual({ title: "mobland", fromStart: false, season: undefined, episode: undefined });
  expect(parseVoice("the boys season two episode three")).toMatchObject({ title: "the boys", season: 2, episode: 3 });
  expect(parseVoice("Severance from the beginning")).toMatchObject({ title: "severance", fromStart: true });
  expect(parseVoice("slow horses series one episode four")).toMatchObject({ title: "slow horses", season: 1, episode: 4 });
  expect(parseVoice("mobland episode 5")).toMatchObject({ title: "mobland", episode: 5, season: undefined });
  expect(parseVoice("next episode of mobland")).toMatchObject({ title: "mobland" });
  expect(parseVoice("dune", "start")).toMatchObject({ title: "dune", fromStart: true });
});

test("a show carries on from where you are", async ({ page, openTv }) => {
  await page.addInitScript(app);
  await openTv(`/search`);
  // The mock's first show: find its title from the search page's data.
  const title = await page.evaluate(async () => {
    const r = await fetch("/api/search?q=a").then((x) => x.json());
    return r.shows[1].title as string;
  });
  await page.goto(`/voice?q=${encodeURIComponent(title.toLowerCase())}&tv=1`);
  await expect.poll(() => page.evaluate(() => (window as unknown as W).__played?.title)).toBe(title);
  // The show's page is left under the player.
  await expect(page).toHaveURL(/\/item\/\d+/);
});

test("a film plays, and an unknown title goes to search", async ({ page, openTv }) => {
  await page.addInitScript(app);
  await openTv(`/search`);
  const film = await page.evaluate(async () => {
    const r = await fetch("/api/search?q=e").then((x) => x.json());
    return r.movies[0].title as string;
  });
  await page.goto(`/voice?q=${encodeURIComponent(film + " from the start")}&tv=1`);
  await expect.poll(() => page.evaluate(() => (window as unknown as W).__played?.title)).toBe(film);
  expect(await page.evaluate(() => (window as unknown as W).__played?.startSeconds)).toBe(0);

  await page.goto(`/voice?q=zzzz%20nothing%20like%20this&tv=1`);
  await expect(page).toHaveURL(/\/search\?q=zzzz/);
  await expect(page.locator(".tv-toast")).toContainText("Couldn't find");
});
