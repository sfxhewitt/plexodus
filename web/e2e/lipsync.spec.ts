// Lip sync: this TV's own offset in Settings (kept by the app), and each
// file's saved fix handed to the app's player.
import { expect, test } from "./fixtures";
import { waitForFocus } from "./focus";
import { ids } from "./mock";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

test.afterEach(({ unhandled }) => {
  expect(unhandled, "API calls the mock doesn't answer (add them to e2e/mock.ts)").toEqual([]);
});

type W = { __delay: number; __calibrated?: boolean; __phone?: boolean; __played?: { fileId: number; audioOffsetMs: number } };

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
    calibrateWithPhone: () => { w.__phone = true; },
    play(json: string) { w.__played = JSON.parse(json); },
  };
};

test("Settings sets this TV's lip sync offset and opens calibration", async ({ page, openTv }) => {
  await page.addInitScript(app, "recap,avsync");
  await openTv("/settings/playback");
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

test("Fix with my phone opens the phone calibration on apps that have it", async ({ page, openTv }) => {
  await page.addInitScript(app, "recap,avsync,avsync-phone");
  await openTv("/settings/playback");
  await waitForFocus(page);
  await expect(page.locator('[data-fk="lipsync-calibrate"]')).toHaveText("Calibrate by eye");
  await page.locator('[data-fk="lipsync-phone"]').focus();
  await page.keyboard.press("Enter");
  expect(await page.evaluate(() => (window as unknown as W).__phone)).toBe(true);
});

test("apps without phone calibration don't offer it", async ({ page, openTv }) => {
  await page.addInitScript(app, "recap,avsync");
  await openTv("/settings/playback");
  await waitForFocus(page);
  await expect(page.locator('[data-fk="lipsync-calibrate"]')).toHaveText("Calibrate");
  await expect(page.locator('[data-fk="lipsync-phone"]')).toHaveCount(0);
});

test("an older app is asked to update instead", async ({ page, openTv }) => {
  await page.addInitScript(app, "recap");
  await openTv("/settings/playback");
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

test("Settings asks an app that can update itself to check for updates", async ({ page, openTv }) => {
  await page.addInitScript(app, "recap,avsync,update");
  await page.addInitScript(() => {
    const w = window as unknown as { PlexodusNative: Record<string, unknown>; __checked?: boolean };
    w.PlexodusNative.checkForUpdates = () => { w.__checked = true; };
  });
  await openTv("/settings/app");
  await waitForFocus(page);
  await page.locator('[data-fk="app-update"]').focus();
  await page.keyboard.press("Enter");
  expect(await page.evaluate(() => (window as unknown as { __checked?: boolean }).__checked)).toBe(true);
});

test("older apps get no update button", async ({ page, openTv }) => {
  await page.addInitScript(app, "recap,avsync");
  await openTv("/settings/app");
  await waitForFocus(page);
  await expect(page.locator('[data-fk="app-update"]')).toHaveCount(0);
});

// Phones and tablets get the web UI, where Settings was admin-only: anyone in
// the app can now reach this device's lip sync there, by touch.
test("a tablet sets its own lip sync offset in Settings", async ({ page }) => {
  await page.addInitScript((f) => {
    const w = window as unknown as Record<string, unknown> & W;
    w.__delay = 0;
    w.PlexodusNative = {
      isTv: () => false, version: () => "test", platform: () => "android", canEmbed: () => true,
      changeServer() {}, features: () => f,
      audioDelay: () => w.__delay,
      setAudioDelay: (ms: number) => { w.__delay = ms; },
      calibrateAudio: () => { w.__calibrated = true; },
    calibrateWithPhone: () => { w.__phone = true; },
      play() {},
    };
  }, "recap,avsync");
  // A friend's account, not the admin: the gear is there only for lip sync.
  await page.route("**/api/me", (r) => r.fulfill({ json: {
    id: 2, username: "friend", isAdmin: false, allLibraries: true, libraries: [], accountId: 2, isProfile: false,
    name: "friend", avatar: "", color: "cyan", kids: false,
  } }));
  await page.goto("/settings?tv=0");
  const section = page.getByTestId("device-lipsync");
  await expect(section).toContainText("In sync");
  await section.getByRole("button", { name: "Sound later" }).click();
  await section.getByRole("button", { name: "Sound later" }).click();
  await expect(section).toContainText("Sound 40 ms later");
  expect(await page.evaluate(() => (window as unknown as W).__delay)).toBe(40);
  await section.getByRole("button", { name: "Reset" }).click();
  expect(await page.evaluate(() => (window as unknown as W).__delay)).toBe(0);
  await section.getByRole("button", { name: "Calibrate" }).click();
  expect(await page.evaluate(() => (window as unknown as W).__calibrated)).toBe(true);
  await expect(page.locator('a[href="/settings"]')).toBeVisible();
});
