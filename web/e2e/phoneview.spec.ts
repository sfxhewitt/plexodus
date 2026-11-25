// "Use the phone-style interface" on a TV must have an obvious way back that
// the remote reaches: a focused "Back to TV view" button, and Back on Home.
import { expect, test } from "./fixtures";
import { waitForFocus } from "./focus";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

const tvBox = () => {
  (window as unknown as Record<string, unknown>).PlexodusNative = {
    isTv: () => true, version: () => "test", platform: () => "android", canEmbed: () => true,
    changeServer() {}, exit() {}, features: () => "", play() {},
  };
};

async function toPhoneView(page: import("@playwright/test").Page, openTv: (p?: string) => Promise<void>) {
  await page.addInitScript(tvBox);
  await openTv("/settings/screen");
  await waitForFocus(page);
  await page.locator('[data-fk="classic"]').focus();
  await page.keyboard.press("Enter");
  const back = page.getByTestId("back-to-tv");
  await expect(back).toBeVisible();
  await expect(back).toBeFocused();
  return back;
}

test("phone-style view on a TV offers a focused Back to TV view button", async ({ page, openTv }) => {
  await toPhoneView(page, openTv);
  await page.screenshot({ path: "test-results/phone-view-on-tv.png" });
  await Promise.all([page.waitForEvent("load"), page.keyboard.press("Enter")]);
  await waitForFocus(page);
  await expect(page.locator(".tv-page, [data-fk]").first()).toBeVisible();
  await expect(page.getByTestId("back-to-tv")).toHaveCount(0);
});

test("Back on the phone-style Home returns to the TV view", async ({ page, openTv }) => {
  await toPhoneView(page, openTv);
  const nav = page.waitForEvent("load");
  const r = await page.evaluate(() => (window as unknown as { __plexodusBack: () => string }).__plexodusBack());
  expect(r).toBe("handled");
  await nav;
  await waitForFocus(page);
  await expect(page.getByTestId("back-to-tv")).toHaveCount(0);
});

test("browsers without the TV flag show no Back to TV view button", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("plexodus.tv", "0"));
  await page.goto("/");
  await expect(page.locator(".nav")).toBeVisible();
  await expect(page.getByTestId("back-to-tv")).toHaveCount(0);
});
