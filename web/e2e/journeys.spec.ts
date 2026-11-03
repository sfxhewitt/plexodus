// What someone with a remote actually does, screen by screen, with screenshots.
import { expect, test } from "./fixtures";
import { focused, press, snap, waitForFocus, walk } from "./focus";
import { ids, MOVIES_LIB } from "./mock";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

test.afterEach(({ unhandled }) => {
  expect(unhandled, "API calls the mock doesn't answer (add them to e2e/mock.ts)").toEqual([]);
});

test("home: rows, sidebar and back again", async ({ page, openTv }, info) => {
  await openTv("/");
  let f = await waitForFocus(page, `c${ids.movie + 3}`); // first Continue Watching card
  await snap(page, info, "01 home");

  // Down through every row to the bottom, then back up to the first.
  const down = await walk(page, "down");
  expect(down.length, "Down should visit all five rows").toBe(5);
  await snap(page, info, "02 last row");
  const up = await walk(page, "up");
  expect(up.at(-1)!.fk).toBe(f.fk);

  // Along a row and back.
  await press(page, "down");
  f = await press(page, "down"); // Recently Added Movies
  const along = await walk(page, "right");
  expect(along.at(-1)!.fk, "Right should reach See all at the end of the row, and stop there").toBe("more-Recently Added Movies");
  await snap(page, info, "03 end of row");

  // Left all the way: back to the first card, then into the sidebar on Home.
  const left = await walk(page, "left");
  expect(left.at(-2)!.fk).toBe(f.fk);
  expect(left.at(-1)!.fk).toBe("nav-home");
  await snap(page, info, "04 sidebar open");
  expect((await press(page, "left")).fk, "Left in the sidebar stays put").toBe("nav-home");
  expect((await press(page, "up")).fk).toBe("nav-search");
  expect((await press(page, "up")).fk, "Up at the top of the sidebar stays put").toBe("nav-search");
  const back = await press(page, "right");
  expect(back.fk, "Right leaves the sidebar where you were").toBe(f.fk);
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
  expect(along.map((x) => x.fk)).toEqual(["play", "watched"]);
  expect((await press(page, "down")).fk, "nothing below the buttons: stay").toBe("watched");
});

test("library: grid, filters and the genre picker", async ({ page, openTv }, info) => {
  await openTv(`/library/${MOVIES_LIB}`);
  await waitForFocus(page, /^c\d+$/);
  await snap(page, info, "01 library");
  const down = await walk(page, "down", 6);
  expect(down.length).toBeGreaterThan(4);
  await snap(page, info, "02 scrolled down");
  await walk(page, "up");
  expect((await focused(page)).fk, "Up from the top row reaches the filters").toMatch(/^(f-|sort|order|genre)/);
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

test("settings", async ({ page, openTv }, info) => {
  await openTv("/settings");
  await waitForFocus(page, "reload");
  await snap(page, info, "01 settings");
});
