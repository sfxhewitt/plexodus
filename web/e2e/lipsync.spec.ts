// Lip sync: this TV's own offset in Settings (kept by the app), and each
// file's saved fix handed to the app's player.
import { expect, test } from "./fixtures";
import { waitForFocus } from "./focus";
import { ids } from "./mock";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

test.afterEach(({ unhandled }) => {
  expect(unhandled, "API calls the mock doesn't answer (add them to e2e/mock.ts)").toEqual([]);
});

type W = { __delay: number; __calibrated?: boolean; __played?: { fileId: number; audioOffsetMs: number } };

// Runs in the page: addInitScript passes `features` as its argument.
const app = (features: string) => {
  const w = window as unknown as Record<string, unknown> & W;
  w.__delay = 0;
  w.PlexodusNative = {
    isTv: () => true, version: () => "test", platform: () => "android", canEmbed: () => true,
    changeServer() {}, exit() {},
    features: () => features,
    audioDelay: () => w.__delay,
    setAudioDelay: (ms: number) => { w.__delay = ms; },
    calibrateAudio: () => { w.__calibrated = true; },
    play(json: string) { w.__played = JSON.parse(json); },
  };
};

test("Settings sets this TV's lip sync offset and opens calibration", async ({ page, openTv }) => {
  await page.addInitScript(app, "recap,avsync");
  await openTv("/settings");
  await waitForFocus(page);
  const later = page.locator('[data-fk="lipsync-later"]');
  await later.focus();
  await page.keyboard.press("Enter");
  await page.keyboard.press("Enter");
  await expect(page.locator(".tv-lib-prefs").filter({ hasText: "Lip sync" })).toContainText("Sound 40 ms later");
  expect(await page.evaluate(() => (window as unknown as W).__delay)).toBe(40);

  await page.locator('[data-fk="lipsync-calibrate"]').focus();
  await page.keyboard.press("Enter");
  expect(await page.evaluate(() => (window as unknown as W).__calibrated)).toBe(true);
  // The app reports the calibrated value when its player closes.
  await page.evaluate(() => {
    (window as unknown as W).__delay = -60;
    window.dispatchEvent(new CustomEvent("plexodus:audio-delay", { detail: -60 }));
  });
  await expect(page.locator(".tv-lib-prefs").filter({ hasText: "Lip sync" })).toContainText("Sound 60 ms earlier");
});

test("an older app is asked to update instead", async ({ page, openTv }) => {
  await page.addInitScript(app, "recap");
  await openTv("/settings");
  await waitForFocus(page);
  await expect(page.locator(".tv-lib-prefs").filter({ hasText: "Lip sync" })).toContainText("Update the Plexodus app");
  await expect(page.locator('[data-fk="lipsync-calibrate"]')).toHaveCount(0);
});

test("the file's saved fix goes to the app's player", async ({ page, openTv }) => {
  await page.addInitScript(app, "recap,avsync");
  await openTv(`/item/${ids.movie}`);
  await waitForFocus(page, "play");
  await page.keyboard.press("Enter");
  await expect.poll(() => page.evaluate(() => (window as unknown as W).__played?.audioOffsetMs)).toBe(-120);
  expect(await page.evaluate(() => (window as unknown as W).__played?.fileId)).toBeGreaterThan(0);
});
