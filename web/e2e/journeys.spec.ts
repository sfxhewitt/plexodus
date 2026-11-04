// What someone with a remote actually does, screen by screen, with screenshots.
import { expect, test } from "./fixtures";
import { focused, press, snap, waitForFocus, walk } from "./focus";
import { ids, MOVIES_LIB } from "./mock";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

test.afterEach(({ unhandled }) => {
  expect(unhandled, "API calls the mock doesn't answer (add them to e2e/mock.ts)").toEqual([]);
});

test("home: hero, rows, tab bar and back again", async ({ page, openTv }, info) => {
  await openTv("/");
  let f = await waitForFocus(page, "hero-play");
  await snap(page, info, "01 home hero");

  // Down into the rows (Continue Watching first), through every row, and back up to the hero.
  const cw = await press(page, "down");
  expect(cw.fk, "Down from the hero reaches Continue Watching").toBe(`c${ids.movie + 3}`);
  await snap(page, info, "02 first row");
  const down = await walk(page, "down");
  expect(down.length, "Down should visit all five rows").toBe(5);
  await snap(page, info, "03 last row");
  const up = await walk(page, "up");
  expect(up.at(-2)!.fk, "Up from the rows returns to the hero").toBe(f.fk);
  expect(up.at(-1)!.fk, "...and then the Home tab").toBe("nav-home");
  expect((await press(page, "down")).fk, "Down from the tab bar goes back into the hero").toBe(f.fk);

  // Along a row and back.
  await press(page, "down");
  await press(page, "down");
  f = await press(page, "down"); // Recently Added Movies
  const along = await walk(page, "right");
  expect(along.at(-1)!.fk, "Right should reach See all at the end of the row, and stop there").toBe("more-Recently Added Movies");
  await snap(page, info, "04 end of row");
  const left = await walk(page, "left");
  expect(left.at(-1)!.fk, "Left stops at the start of the row").toBe(f.fk);

  // Up to the top: hero, then the tab bar on Home.
  const top = await walk(page, "up");
  expect(top.at(-2)!.fk).toBe("hero-play");
  expect(top.at(-1)!.fk, "Up from the hero lands on the Home tab").toBe("nav-home");
  await snap(page, info, "05 tab bar");
  expect((await press(page, "up")).fk, "Up in the tab bar stays put").toBe("nav-home");
  expect((await press(page, "left")).fk).toBe("nav-search");
  expect((await press(page, "left")).fk, "Left at the start of the tab bar stays put").toBe("nav-search");
  await press(page, "right");
  const tabs = await walk(page, "right");
  expect(tabs.at(-1)!.fk, "Right ends on the settings avatar").toBe("nav-settings");
  const back = await press(page, "down");
  expect(back.fk, "Down leaves the tab bar back into the page").toBe("hero-play");
});

test("show page: Down leaves the hero into the seasons", async ({ page, openTv }, info) => {
  await openTv(`/item/${ids.show}`);
  await waitForFocus(page, "play");
  await snap(page, info, "01 show");
  const f = await press(page, "down");
  expect(f.fk, "DPAD_DOWN from the hero buttons must reach the Seasons row").toBe(`c${ids.season}`);
  await snap(page, info, "02 seasons row");
  expect((await press(page, "up")).fk, "Up returns to the button you left").toBe("play");

  // Into a season: tabs, then episodes.
  await press(page, "down");
  await page.keyboard.press("Enter");
  await waitForFocus(page, "play");
  await snap(page, info, "03 season");
  // The buttons wrap onto two lines at this size, so Down may stop on "Go to show" first.
  let g = await press(page, "down");
  if (g.fk === "show") g = await press(page, "down");
  expect(g.fk, "Down from the hero lands on this season's tab").toBe(`s${ids.season}`);
  g = await press(page, "down");
  expect(g.fk, "then on the episode to watch next").toBe(`c${ids.episode}`);
  await walk(page, "right");
  await snap(page, info, "04 last episode");

  // Back returns to the show page with the season card focused.
  await page.keyboard.press("Escape");
  await waitForFocus(page, `c${ids.season}`);
});

test("episode page: Down leaves the hero into the other episodes", async ({ page, openTv }, info) => {
  await openTv(`/item/${ids.episode}`);
  await waitForFocus(page, "play");
  const down = await walk(page, "down");
  expect(down.at(-1)!.fk, "Down from the buttons reaches the episode itself in the row").toBe(`c${ids.episode}`);
  await snap(page, info, "01 episodes row");
});

test("movie page: buttons in a line, nothing below", async ({ page, openTv }, info) => {
  await openTv(`/item/${ids.movie}`);
  await waitForFocus(page, "play");
  await snap(page, info, "01 movie");
  const along = await walk(page, "right");
  expect(along.map((x) => x.fk)).toEqual(["play", "watched", "watchlist"]);
  expect((await press(page, "down")).fk, "nothing below the buttons: stay").toBe("watchlist");
  await press(page, "Enter");
  await expect(page.locator('[data-fk="watchlist"]')).toHaveText("On your Watchlist");
});

test("library: grid, filters and the genre picker", async ({ page, openTv }, info) => {
  await openTv(`/library/${MOVIES_LIB}`);
  await waitForFocus(page, /^c\d+$/);
  await snap(page, info, "01 library");
  const down = await walk(page, "down", 6);
  expect(down.length).toBeGreaterThan(4);
  await snap(page, info, "02 scrolled down");
  const up = await walk(page, "up");
  expect(up.at(-2)!.fk, "Up from the top row reaches the filters").toMatch(/^(f-|sort|order|genre)/);
  expect(up.at(-1)!.fk, "...and then this library's tab").toBe(`nav-lib${MOVIES_LIB}`);
  expect((await press(page, "down")).fk, "Down leaves the tab bar into the filters").toMatch(/^(f-|sort|order|genre)/);
  await walk(page, "right");
  expect((await focused(page)).fk).toBe("genre");

  // The picker is a modal: focus stays inside it, Back closes it and returns to the button.
  await page.keyboard.press("Enter");
  await waitForFocus(page, "g-");
  await walk(page, "down");
  expect((await press(page, "left")).fk, "Left doesn't escape the modal").toMatch(/^g-/);
  await snap(page, info, "03 genre picker");
  await page.keyboard.press("Escape");
  await waitForFocus(page, "genre");
});

test("search: keyboard to results", async ({ page, openTv }, info) => {
  await openTv("/search");
  await waitForFocus(page, "k-a");
  for (const k of ["k-h", "k-a"]) {
    await page.locator(`[data-fk="${k}"]`).focus();
    await page.keyboard.press("Enter");
  }
  await expect(page.locator(".tv-row")).not.toHaveCount(0);
  await snap(page, info, "01 typed");
  const f = await walk(page, "right", 12);
  expect(f.some((x) => /^c\d+$/.test(x.fk)), "Right from the keyboard reaches the results").toBe(true);
  await snap(page, info, "02 results");
});

test("search: ask in plain English", async ({ page, openTv }, info) => {
  await openTv("/search");
  await waitForFocus(page, "k-a");
  await snap(page, info, "01 examples");
  // An example request from the hint, reached by moving right off the keyboard.
  await page.locator('[data-fk="ex-0"]').focus();
  await page.keyboard.press("Enter");
  await expect(page.locator(".tv-ask-title")).toHaveText(/Results for 90s heist films/);
  await expect(page.locator(".tv-row")).toHaveCount(2);
  await expect(page).toHaveURL(/ask=1/);
  await snap(page, info, "02 answer");
  const f = await walk(page, "down", 4);
  expect(f.some((x) => /^c\d+$/.test(x.fk)), "Down from the example reaches the answer rows").toBe(true);

  // Typing again goes back to a title search.
  await page.locator('[data-fk="k-b"]').focus();
  await page.keyboard.press("Enter");
  await expect(page.locator(".tv-ask-title")).toHaveCount(0);
});

test("settings", async ({ page, openTv }, info) => {
  await openTv("/settings");
  await waitForFocus(page, "profiles");
  await snap(page, info, "01 settings");

  // Down to the library choices: hide TV Shows from the menu.
  const tabs = page.locator(".tv-nav .tv-tab-btn");
  await expect(tabs.filter({ hasText: "TV Shows" })).toHaveCount(1);
  let f = await press(page, "down");
  expect(f.fk, "Down from the buttons reaches the first library").toBe("lib-1-menu");
  f = await press(page, "down");
  expect(f.fk).toBe("lib-2-menu");
  expect((await press(page, "right")).fk, "Right moves to the Home switch").toBe("lib-2-home");
  await press(page, "left");
  await press(page, "Enter");
  await expect(page.locator('[data-fk="lib-2-menu"]')).toHaveAttribute("aria-pressed", "false");
  await expect(tabs.filter({ hasText: "TV Shows" }), "hidden library leaves the tab bar").toHaveCount(0);
  expect(await focused(page).then((x) => x.fk), "focus stays on the switch").toBe("lib-2-menu");
  await snap(page, info, "02 library hidden");

  // And back again.
  await press(page, "Enter");
  await expect(tabs.filter({ hasText: "TV Shows" })).toHaveCount(1);
});
