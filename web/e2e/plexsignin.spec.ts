// Sign in with Plex on the TV: the button is focused first, shows a code for
// plex.tv/link, and signs in once Plex approves it.
import { expect, test } from "./fixtures";
import { focused, waitForFocus } from "./focus";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

test.afterEach(({ unhandled }) => {
  expect(unhandled, "API calls the mock doesn't answer (add them to e2e/mock.ts)").toEqual([]);
});

test("sign in with Plex shows a plex.tv/link code and lands on Home", async ({ page }) => {
  let signedIn = false;
  let approved = false;
  await page.route("**/api/status", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ setupRequired: false, plexSignIn: true }) }));
  await page.route("**/api/me", (route) =>
    signedIn ? route.fallback() : route.fulfill({ status: 401, contentType: "application/json", body: '{"error":"sign in required"}' }));
  await page.route("**/api/auth/plex/pin", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ id: 42, code: "K7QX", authUrl: "https://app.plex.tv/auth#?x", expiresIn: 900 }) }));
  await page.route("**/api/auth/plex/pin/42", (route) => {
    if (!approved) return route.fulfill({ status: 200, contentType: "application/json", body: '{"pending":true}' });
    signedIn = true;
    return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({
      id: 1, username: "daniel", isAdmin: true, allLibraries: true, libraries: [], accountId: 1, isProfile: false,
      name: "daniel", avatar: "", color: "cyan", kids: false }) });
  });

  await page.goto("/?tv=1");
  await waitForFocus(page);
  expect((await focused(page)).text).toContain("Sign in with Plex");
  await page.screenshot({ path: "test-results/plex-signin-button.png" });
  await page.keyboard.press("Enter");
  await expect(page.locator(".tv-plex-code")).toHaveText("K7QX");
  await expect(page.getByText("plex.tv/link")).toBeVisible();
  expect((await focused(page)).text).toContain("Cancel");
  await page.screenshot({ path: "test-results/plex-signin-code.png" });

  approved = true;
  await waitForFocus(page, "hero-play");
  await page.screenshot({ path: "test-results/plex-signin-home.png" });
});
