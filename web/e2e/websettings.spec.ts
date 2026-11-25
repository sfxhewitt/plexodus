// The web Settings page: categories down the left on a computer, a list you
// tap into on a phone, and every setting still doing its job.
import { expect, test } from "./fixtures";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

type Put = Record<string, unknown>;

/** Answers the admin-only endpoints the Settings page uses and records what it saves. */
async function adminApi(page: import("@playwright/test").Page) {
  const puts: { path: string; body: Put }[] = [];
  const settings = {
    tmdbKeySet: true, tmdbKeyMasked: "abcd…wxyz", tmdbKeyFromEnv: false, apiKey: "test-api-key",
    anthropicKeySet: false, anthropicKeyMasked: "", anthropicKeyFromEnv: false, askModel: "claude",
    detectMarkers: true, upNextMinMinutes: 10,
    autoUpdate: { enabled: true, fromEnv: false, version: "main-test", installed: true, last: { state: "current", message: "Up to date.", checkedAt: Date.now() / 1000 } },
  };
  const plex = {
    url: "http://127.0.0.1:32400", tokenSet: true, tokenFromEnv: false, pathMap: "",
    status: { running: false, phase: "", section: "", items: 0, files: 0, watched: 0, markers: 0, finished: "0001-01-01T00:00:00Z" },
    sync: { enabled: true, intervalMinutes: 5, running: false, lastRun: new Date().toISOString(), lastOk: new Date().toISOString(), tookMs: 800, checked: 3, updated: 1, extras: {} },
    signIn: { enabled: false, publish: false, publicUrl: "", seenUrl: "http://localhost:5180", published: "", status: { url: "", ok: true, at: "" } },
  };
  const users = [
    { id: 1, username: "daniel", isAdmin: true, allLibraries: true, libraries: [], followPlex: false },
    { id: 2, username: "friend", isAdmin: false, allLibraries: false, libraries: [1], followPlex: false },
  ];
  const body = (r: import("@playwright/test").Route) => (r.request().postDataJSON() || {}) as Put;
  await page.route("**/api/settings", (r) => {
    if (r.request().method() === "PUT") {
      const b = body(r);
      puts.push({ path: "/api/settings", body: b });
      if (typeof b.upNextMinMinutes === "number") settings.upNextMinMinutes = b.upNextMinMinutes;
      if (typeof b.detectMarkers === "boolean") settings.detectMarkers = b.detectMarkers;
      if (typeof b.autoUpdate === "boolean") settings.autoUpdate.enabled = b.autoUpdate;
      if (typeof b.anthropicKey === "string") settings.anthropicKeySet = !!b.anthropicKey;
    }
    return r.fulfill({ json: settings });
  });
  await page.route("**/api/plex", (r) => {
    if (r.request().method() === "PUT") {
      const b = body(r);
      puts.push({ path: "/api/plex", body: b });
      if (typeof b.syncEnabled === "boolean") plex.sync.enabled = b.syncEnabled;
      if (typeof b.syncIntervalMinutes === "number") plex.sync.intervalMinutes = b.syncIntervalMinutes;
      if (typeof b.signInEnabled === "boolean") plex.signIn.enabled = b.signInEnabled;
    }
    return r.fulfill({ json: plex });
  });
  await page.route("**/api/scan/status", (r) => r.fulfill({ json: [] }));
  await page.route("**/api/libraries/*/scan*", (r) => (puts.push({ path: new URL(r.request().url()).pathname, body: {} }), r.fulfill({ json: {} })));
  await page.route("**/api/invites", (r) => r.fulfill({ json: [] }));
  await page.route("**/api/users", (r) => {
    if (r.request().method() === "POST") {
      const b = body(r);
      puts.push({ path: "/api/users", body: b });
      const u = { id: 3, username: String(b.username), isAdmin: !!b.isAdmin, allLibraries: false, libraries: [], followPlex: false };
      users.push(u);
      return r.fulfill({ json: u });
    }
    return r.fulfill({ json: users });
  });
  await page.route("**/api/users/*/access", (r) => (puts.push({ path: new URL(r.request().url()).pathname, body: body(r) }), r.fulfill({ json: {} })));
  return puts;
}

const SECTIONS = ["libraries", "plex", "playback", "metadata", "people", "server"];

test("computer: categories down the left, one shown at a time, and settings save", async ({ page, unhandled }, info) => {
  const puts = await adminApi(page);
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/settings?tv=0");
  const nav = page.locator(".settings-nav");
  await expect(nav.locator("a")).toHaveCount(SECTIONS.length);
  // Opens on the first category; nothing else is on the page.
  await expect(page.locator(".settings-title")).toHaveText("Libraries");
  await expect(nav.locator("a.on")).toContainText("Libraries");
  await expect(page.getByRole("heading", { name: "Users" })).toHaveCount(0);
  await expect(page.locator(".lib-row").first()).toBeVisible();
  await page.screenshot({ path: info.outputPath("web-01-libraries.png") });
  await page.locator(".lib-row").first().getByRole("button", { name: "Scan", exact: true }).click();
  await expect.poll(() => puts.at(-1)?.path).toMatch(/^\/api\/libraries\/\d+\/scan$/);

  await nav.getByRole("link", { name: /Plex/ }).click();
  await expect(page).toHaveURL(/\/settings\/plex/);
  for (const h of ["Import from Plex", "Watch status from Plex", "Sign in with Plex", "Share to Plex"]) {
    await expect(page.getByRole("heading", { name: h })).toBeVisible();
  }
  await page.locator(".settings-section").filter({ hasText: "Watch status from Plex" }).locator("select").selectOption("30");
  await expect.poll(() => puts.at(-1)).toEqual({ path: "/api/plex", body: { syncIntervalMinutes: 30 } });
  await page.getByText("Allow Sign in with Plex").click();
  await expect.poll(() => puts.at(-1)).toEqual({ path: "/api/plex", body: { signInEnabled: true } });
  await page.screenshot({ path: info.outputPath("web-02-plex.png") });

  await nav.getByRole("link", { name: /Playback/ }).click();
  await page.locator("#upnext-min").selectOption("20");
  await expect.poll(() => puts.at(-1)).toEqual({ path: "/api/settings", body: { upNextMinMinutes: 20 } });
  await page.getByText("Find recaps, intros and credits on first play").click();
  await expect.poll(() => puts.at(-1)).toEqual({ path: "/api/settings", body: { detectMarkers: false } });
  await page.screenshot({ path: info.outputPath("web-03-playback.png") });

  await nav.getByRole("link", { name: /Metadata and search/ }).click();
  await expect(page.getByRole("heading", { name: "Metadata", exact: true })).toBeVisible();
  const ask = page.locator(".settings-section").filter({ hasText: "Plain-English search" });
  await ask.locator("input").fill("sk-ant-test");
  await ask.getByRole("button", { name: "Save key" }).click();
  await expect.poll(() => puts.at(-1)).toEqual({ path: "/api/settings", body: { anthropicKey: "sk-ant-test" } });
  await expect(ask.getByRole("button", { name: "Remove key" })).toBeVisible();

  await nav.getByRole("link", { name: /People/ }).click();
  await expect(page.getByRole("heading", { name: "Share libraries" })).toBeVisible();
  const users = page.locator(".settings-section").filter({ has: page.getByRole("heading", { name: "Users" }) });
  await expect(users.locator(".user-row")).toHaveCount(2);
  await users.getByPlaceholder("Username").fill("sam");
  await users.getByPlaceholder("Password").fill("secret-pass");
  await users.getByRole("button", { name: "Add user" }).click();
  await expect(users.locator(".user-row")).toHaveCount(3);
  // A new friend's library choice opens straight away.
  await expect(page.locator(".modal")).toContainText("Libraries sam can see");
  await page.locator(".modal").getByRole("button", { name: "Close" }).click();
  await page.screenshot({ path: info.outputPath("web-04-people.png") });

  await nav.getByRole("link", { name: /Server/ }).click();
  await expect(page.getByRole("heading", { name: "Updates" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Integrations" })).toBeVisible();
  await page.getByText("Update automatically").click();
  await expect.poll(() => puts.at(-1)).toEqual({ path: "/api/settings", body: { autoUpdate: false } });
  await page.screenshot({ path: info.outputPath("web-05-server.png") });

  // Each category has its own address, so a reload or a link lands on it.
  await page.goto("/settings/server?tv=0");
  await expect(page.locator(".settings-title")).toHaveText("Server");
  expect(unhandled, "API calls the mock doesn't answer").toEqual([]);
});

test("phone: a list of categories, each opening with a way back", async ({ page, unhandled }, info) => {
  await adminApi(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/settings?tv=0");
  const nav = page.locator(".settings-nav");
  await expect(nav.locator("a")).toHaveCount(SECTIONS.length);
  await expect(page.locator(".settings-pane"), "the list alone, not a long page").toHaveCount(0);
  for (const a of await nav.locator("a").all()) {
    expect((await a.boundingBox())!.height, "rows are easy to tap").toBeGreaterThanOrEqual(56);
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), "no sideways scroll").toBe(true);
  await page.screenshot({ path: info.outputPath("phone-01-list.png") });

  for (const key of SECTIONS) {
    await nav.locator(`a[href="/settings/${key}"]`).click();
    await expect(page).toHaveURL(new RegExp(`/settings/${key}`));
    await expect(page.locator(`.settings-pane[data-section="${key}"]`)).toBeVisible();
    await expect(nav).toHaveCount(0);
    await page.waitForTimeout(300);
    // Name whatever sticks out, so a failure says what to fix. Wider letters stand in for other fonts.
    await page.addStyleTag({ content: "*{letter-spacing:0.12em!important}" });
    const wide = await page.evaluate(() => [...document.querySelectorAll<HTMLElement>(".settings-pane *")]
      .filter((el) => el.getBoundingClientRect().right > window.innerWidth + 1 && !el.closest(".code"))
      .map((el) => `${el.tagName.toLowerCase()}.${el.className} "${el.innerText.slice(0, 30)}"`).slice(0, 5));
    expect(wide, `${key}: nothing runs off the side`).toEqual([]);
    await page.screenshot({ path: info.outputPath(`phone-02-${key}.png`) });
    await page.locator(".settings-back").click();
    await expect(page).toHaveURL(/\/settings(\?|$)/);
    await expect(nav).toBeVisible();
  }
  // The browser's Back does the same.
  await nav.locator('a[href="/settings/people"]').click();
  await expect(page.locator(".settings-title")).toHaveText("People");
  await page.goBack();
  await expect(nav).toBeVisible();
  expect(unhandled, "API calls the mock doesn't answer").toEqual([]);
});

test("someone who isn't the admin gets no category list", async ({ page }) => {
  await page.route("**/api/me", (r) => r.fulfill({ json: {
    id: 2, username: "friend", isAdmin: false, allLibraries: true, libraries: [], accountId: 2, isProfile: false,
    name: "friend", avatar: "", color: "cyan", kids: false,
  } }));
  await page.goto("/settings?tv=0");
  await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();
  await expect(page.locator(".settings-nav")).toHaveCount(0);
  await expect(page.locator(".settings")).toContainText("nothing to set up here");
});
