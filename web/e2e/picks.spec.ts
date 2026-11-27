// Reel Pick in the film library: mood filters, Surprise me and Help me choose.
import { expect, test } from "./fixtures";
import { focused, press, snap, waitForFocus } from "./focus";
import { MOVIES_LIB, SHOWS_LIB } from "./mock";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

test.afterEach(({ unhandled }) => {
  expect(unhandled, "API calls the mock doesn't answer (add them to e2e/mock.ts)").toEqual([]);
});

const count = async (page: import("@playwright/test").Page) => Number((await page.locator(".tv-count").innerText()).replace(/,/g, "").match(/\d+/)![0]);

test("mood, content, feel and never-show filters narrow the library", async ({ page, openTv }, info) => {
  await openTv(`/library/${MOVIES_LIB}`);
  await waitForFocus(page, /^c\d+$/);
  const all = await count(page);
  await page.locator('[data-fk="filters"]').focus();
  await page.keyboard.press("Enter");
  // The panel opens on Mood in a film library.
  await waitForFocus(page, "cat-mood");
  expect((await press(page, "right")).fk).toBe("o-mood-");
  expect((await press(page, "down")).fk).toBe("o-mood-fg");
  await page.keyboard.press("Enter");
  await expect(page.locator('.tv-fp-cat[data-fk="cat-mood"] .v')).toHaveText("Feel-good");
  // Content takes several choices at once.
  await page.locator('[data-fk="cat-content"]').focus();
  await press(page, "right");
  await page.locator('[data-fk="o-content-noscares"]').focus();
  await page.keyboard.press("Enter");
  await page.locator('[data-fk="o-content-noviolence"]').focus();
  await page.keyboard.press("Enter");
  await expect(page.locator('.tv-fp-cat[data-fk="cat-content"] .v')).toHaveText("No scares, No graphic violence");
  // A feel slider steps on each press: any, low, middle, high, any.
  await page.locator('[data-fk="cat-feel"]').focus();
  expect((await press(page, "right")).fk).toBe("o-feel-v");
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-fk="o-feel-v"] .tv-muted')).toHaveText("Bleak");
  await page.keyboard.press("Enter");
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-fk="o-feel-v"] .tv-muted')).toHaveText("Uplifting");
  expect((await focused(page)).fk, "focus stays on the slider").toBe("o-feel-v");
  await snap(page, info, "01 mood filters");
  // Never show: one press rules a genre out for this profile.
  await page.locator('[data-fk="cat-never"]').focus();
  await press(page, "right");
  await page.locator('[data-fk="o-never-Drama"]').focus();
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-fk="o-never-Drama"] .tv-muted')).toHaveText("✕ Never");
  await expect(page.locator('.tv-fp-cat[data-fk="cat-never"] .v')).toHaveText("No Drama");
  await page.keyboard.press("Escape");
  await waitForFocus(page, "filters");
  await expect(page.locator('[data-fk="filters"]')).toHaveText("Filters · 4");
  for (const fk of ["p-mood", "p-content-noscares", "p-content-noviolence", "p-feel-v", "pg-Drama"]) await expect(page.locator(`[data-fk="${fk}"]`)).toBeVisible();
  expect(await count(page)).toBeLessThan(all);
  await snap(page, info, "02 pills");
  // One OK on a pill removes it; the never-show pill un-saves the genre.
  await page.locator('[data-fk="pg-Drama"]').focus();
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-fk="pg-Drama"]')).toHaveCount(0);
  await page.locator('[data-fk="p-content-noscares"]').focus();
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-fk="p-content-noscares"]')).toHaveCount(0);
  await expect(page.locator('[data-fk="p-content-noviolence"]')).toBeVisible();
  // Best match is a sort like the others.
  await page.locator('[data-fk="sort"]').focus();
  await page.keyboard.press("Enter");
  await page.locator('[data-fk="o-sort-match"]').focus();
  await page.keyboard.press("Enter");
  await page.keyboard.press("Escape");
  await expect(page.locator('[data-fk="sort"]')).toHaveText("Best match");
});

test("a TV library has no film moods or pick buttons", async ({ page, openTv }) => {
  await openTv(`/library/${SHOWS_LIB}`);
  await waitForFocus(page, /^c\d+$/);
  await expect(page.locator('[data-fk="surprise"]')).toHaveCount(0);
  await page.locator('[data-fk="filters"]').focus();
  await page.keyboard.press("Enter");
  await waitForFocus(page, "cat-genre");
  await expect(page.locator('[data-fk="cat-mood"]')).toHaveCount(0);
  await expect(page.locator('[data-fk="cat-never"]')).toBeVisible();
});

test("Surprise me: one pick at a time, Pick another, Not interested, Back", async ({ page, openTv }, info) => {
  await openTv(`/library/${MOVIES_LIB}?mood=la`);
  await waitForFocus(page, /^c\d+$/);
  await page.locator('[data-fk="surprise"]').focus();
  await page.keyboard.press("Enter");
  // Pick another is first and focused, so OK again and again flicks through films.
  await waitForFocus(page, "pk-next");
  const title = () => page.locator(".tv-pick .tv-detail-title").innerText();
  const first = await title();
  await expect(page.locator(".tv-pick .tv-eyebrow")).toHaveText(/Laugh · \d+ more lined up/);
  await expect(page.locator(".tv-pick-why")).toContainText("Fits laugh");
  await snap(page, info, "01 surprise");
  // The buttons are one row, with the quieter three below.
  expect((await press(page, "left")).fk, "nothing sits before Pick another").toBe("pk-next");
  expect((await press(page, "right")).fk).toBe("pk-play");
  expect((await press(page, "right")).fk).toBe("pk-details");
  expect((await press(page, "down")).fk).toMatch(/^pk-(watch|seen|like|dislike|notint)$/);
  expect((await press(page, "down")).fk, "Down doesn't escape the overlay").toMatch(/^pk-(watch|seen|like|dislike|notint)$/);
  await page.locator('[data-fk="pk-next"]').focus();
  await page.keyboard.press("Enter");
  await expect.poll(title).not.toBe(first);
  await waitForFocus(page, "pk-next");
  // Straight on to a third without moving.
  const mid = await title();
  await page.keyboard.press("Enter");
  await expect.poll(title).not.toBe(mid);
  await waitForFocus(page, "pk-next");
  const second = await title();
  await page.locator('[data-fk="pk-notint"]').focus();
  await page.keyboard.press("Enter");
  await expect.poll(title).not.toBe(second);
  await expect(page.locator(".tv-toast")).toContainText("hidden for a year");
  await waitForFocus(page, "pk-next");
  // Back returns to the button it came from.
  await page.keyboard.press("Escape");
  await waitForFocus(page, "surprise");
  await expect(page.locator(".tv-pick-wrap")).toHaveCount(0);
  // Films already suggested rest: the next surprise starts somewhere new.
  await page.keyboard.press("Enter");
  await waitForFocus(page, "pk-next");
  expect([first, mid, second]).not.toContain(await title());
});

test("Help me choose: the quiz, three picks, dismiss and shuffle", async ({ page, openTv }, info) => {
  await openTv(`/library/${MOVIES_LIB}`);
  await waitForFocus(page, /^c\d+$/);
  await page.locator('[data-fk="choose"]').focus();
  await page.keyboard.press("Enter");
  await waitForFocus(page, "qo-stressed");
  await expect(page.locator(".tv-quiz .tv-h1")).toHaveText("How are you feeling right now?");
  await snap(page, info, "01 quiz question");
  await page.keyboard.press("Enter"); // stressed
  await expect(page.locator(".tv-quiz .tv-h1")).toHaveText("Match your mood, or change it?");
  // Back steps back a question rather than leaving.
  await page.keyboard.press("Escape");
  await expect(page.locator(".tv-quiz .tv-h1")).toHaveText("How are you feeling right now?");
  await waitForFocus(page, "qo-stressed");
  // "Not sure" skips the match-or-change question.
  await page.locator('[data-fk="qo-"]').focus();
  await page.keyboard.press("Enter");
  await expect(page.locator(".tv-quiz .tv-h1")).toHaveText("Who's watching?");
  await waitForFocus(page, "qo-solo");
  await page.keyboard.press("Enter");
  await waitForFocus(page, "qo-short");
  await page.keyboard.press("Enter");
  await waitForFocus(page, "qo-classic");
  await page.keyboard.press("Enter");
  // Genres: three-way chips, then Next.
  await expect(page.locator(".tv-quiz .tv-h1")).toHaveText("Anything you never want to see?");
  await page.locator('[data-fk="qg-Crime"]').focus();
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-fk="qg-Crime"]')).toHaveText("✕ Crime");
  await snap(page, info, "02 quiz genres");
  await page.locator('[data-fk="qz-next"]').focus();
  await page.keyboard.press("Enter");
  await waitForFocus(page, "qo-classic");
  await page.keyboard.press("Enter");
  // Results: three picks, the first focused on Play.
  await waitForFocus(page, /^qp-play-\d+$/);
  await expect(page.locator(".tv-quiz-pick")).toHaveCount(3);
  const titles = () => page.locator(".tv-quiz-pick h3").allInnerTexts();
  const before = await titles();
  await snap(page, info, "03 quiz picks");
  // Moving right crosses to the next pick; down reaches its other buttons.
  const top = (await focused(page)).fk!;
  expect((await press(page, "down")).fk).toMatch(new RegExp(`^qp-(info|watch|seen|notint)-${top.split("-")[2]}$`));
  // Seen it clears the top pick: the next moves up and a new one joins.
  await page.locator(`[data-fk="qp-seen-${top.split("-")[2]}"]`).focus();
  await page.keyboard.press("Enter");
  await expect.poll(titles).toEqual([before[1], before[2], expect.any(String)]);
  await waitForFocus(page, /^qp-play-\d+$/);
  // Shuffle brings three new ones.
  await page.locator('[data-fk="qz-shuffle"]').focus();
  await page.keyboard.press("Enter");
  await expect.poll(async () => (await titles()).some((t) => before.includes(t))).toBe(false);
  // Back from the results leaves, to the button it came from, with the saved genre as a pill.
  await page.keyboard.press("Escape");
  await waitForFocus(page, "choose");
  await expect(page.locator('[data-fk="pg-Crime"]')).toBeVisible();
});

test("Back from a pick's page returns to the same picks", async ({ page, openTv }) => {
  await openTv(`/library/${MOVIES_LIB}`);
  await waitForFocus(page, /^c\d+$/);
  // Help me choose: answer the quiz, open the second pick's page, come back.
  await page.locator('[data-fk="choose"]').focus();
  await page.keyboard.press("Enter");
  for (const fk of ["qo-stressed", "qo-match", "qo-solo", "qo-short", "qo-classic"]) {
    await waitForFocus(page, fk);
    await page.keyboard.press("Enter");
  }
  await page.locator('[data-fk="qz-next"]').focus();
  await page.keyboard.press("Enter");
  await waitForFocus(page, "qo-classic");
  await page.keyboard.press("Enter");
  await waitForFocus(page, /^qp-play-\d+$/);
  const titles = () => page.locator(".tv-quiz-pick h3").allInnerTexts();
  const before = await titles();
  expect(before).toHaveLength(3);
  const details = await page.locator(".tv-quiz-pick").nth(1).locator('[data-fk^="qp-info-"]').getAttribute("data-fk");
  await page.locator(`[data-fk="${details}"]`).focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/item\/\d+/);
  await waitForFocus(page, "play");
  await expect(page.locator(".tv-quiz")).toHaveCount(0);
  await page.keyboard.press("Escape");
  // The same three, on the Details button that was pressed: no new draw, no questions.
  await waitForFocus(page, details!);
  expect(await titles()).toEqual(before);
  // And again, after looking at another of them.
  await page.keyboard.press("Enter");
  await waitForFocus(page, "play");
  await page.keyboard.press("Escape");
  await waitForFocus(page, details!);
  expect(await titles()).toEqual(before);
  // Back from the picks then leaves to the library, and it stays left.
  await page.keyboard.press("Escape");
  await waitForFocus(page, "choose");
  await expect(page.locator(".tv-quiz")).toHaveCount(0);
  // Opening it afresh starts at the first question.
  await page.keyboard.press("Enter");
  await expect(page.locator(".tv-quiz .tv-h1")).toHaveText("How are you feeling right now?");
  await page.keyboard.press("Escape");
  await waitForFocus(page, "choose");

  // Surprise me: the same film is waiting after a look at its page.
  await page.locator('[data-fk="surprise"]').focus();
  await page.keyboard.press("Enter");
  await waitForFocus(page, "pk-next");
  await page.keyboard.press("Enter");
  await waitForFocus(page, "pk-next");
  const film = await page.locator(".tv-pick .tv-detail-title").innerText();
  const lined = await page.locator(".tv-pick .tv-eyebrow").textContent();
  await page.locator('[data-fk="pk-details"]').focus();
  await page.keyboard.press("Enter");
  await waitForFocus(page, "play");
  await page.keyboard.press("Escape");
  await waitForFocus(page, "pk-details");
  await expect(page.locator(".tv-pick .tv-detail-title")).toHaveText(film);
  await expect(page.locator(".tv-pick .tv-eyebrow")).toHaveText(lined!);
  await page.keyboard.press("Escape");
  await waitForFocus(page, "surprise");
  // A reload of the library afterwards doesn't bring the picks back.
  await page.reload();
  await waitForFocus(page, /^c\d+$/);
  await expect(page.locator(".tv-pick-wrap")).toHaveCount(0);
});

test.describe("phone and web library", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true });

  const total = async (page: import("@playwright/test").Page) => {
    await expect(page.locator(".lib-head .muted")).toContainText("titles");
    return Number((await page.locator(".lib-head .muted").innerText()).replace(/,/g, "").match(/\d+/)![0]);
  };

  test("the Filters sheet narrows a film library and shows pills", async ({ page }, info) => {
    await page.goto(`/library/${MOVIES_LIB}?tv=0`);
    const all = await total(page);
    await page.getByRole("button", { name: /^Filters/ }).click();
    const sheet = page.getByRole("dialog", { name: "Filters" });
    await sheet.getByRole("button", { name: /^Feel-good/ }).click();
    await sheet.getByRole("button", { name: /^No scares/ }).click();
    await sheet.getByLabel("Humour: Serious to Funny").fill("85");
    await expect(sheet.locator(".pk-dim.set output")).toHaveText("Funny");
    await sheet.getByLabel("Year from").fill("1990");
    await expect(sheet.getByRole("button", { name: /^Show \d/ })).toBeVisible();
    await page.screenshot({ path: info.outputPath("phone-filters.png") });
    // A genre ruled out is saved straight away and shows as a pill too.
    await sheet.getByRole("button", { name: "Mystery: allowed" }).click();
    await expect(sheet.getByRole("button", { name: "Mystery: never show" })).toBeVisible();
    // The button's count is what the library then shows.
    await page.waitForTimeout(500);
    const shows = Number((await sheet.locator(".pk-foot .btn.primary").innerText()).match(/\d+/)?.[0] || 0);
    await sheet.locator(".pk-foot .btn.primary").click();
    await expect(sheet).toBeHidden();
    await expect.poll(() => total(page)).toBe(shows);
    await expect(page.locator(".lib-pills .pk-chip")).toHaveText([/Feel-good/, /No scares/, /Funny/, /From 1990/, /Mystery/]);
    await expect.poll(() => total(page)).toBeLessThan(all);
    await page.screenshot({ path: info.outputPath("phone-library.png") });
    // Each pill takes its own filter off.
    await page.getByRole("button", { name: "Remove filter: Feel-good" }).click();
    await expect(page.locator(".lib-pills .pk-chip")).toHaveCount(4);
    await page.getByRole("button", { name: "Allow Mystery again" }).click();
    await expect(page.locator(".lib-pills .pk-chip")).toHaveCount(3);
  });

  test("Surprise me suggests films and Help me choose gives three picks", async ({ page }, info) => {
    await page.goto(`/library/${MOVIES_LIB}?tv=0`);
    await page.getByRole("button", { name: "Surprise me" }).click();
    const sheet = page.getByRole("dialog", { name: "Surprise me" });
    const first = await sheet.locator(".pk-pick h3").innerText();
    await expect(sheet.locator(".pk-why")).toBeVisible();
    await page.screenshot({ path: info.outputPath("phone-surprise.png") });
    await sheet.getByRole("button", { name: "Pick another" }).click();
    await expect(sheet.locator(".pk-pick h3")).not.toHaveText(first);
    await sheet.getByRole("button", { name: "Watchlist" }).click();
    await expect(sheet.getByRole("status")).toContainText("Added to your Watchlist");
    await page.keyboard.press("Escape");
    await expect(sheet).toBeHidden();

    await page.getByRole("button", { name: "Help me choose" }).click();
    const quiz = page.getByRole("dialog", { name: "Help me choose" });
    await quiz.getByRole("button", { name: /Stressed/ }).click();
    await quiz.getByRole("button", { name: /Change my mood/ }).click();
    await quiz.getByRole("button", { name: /Just me/ }).click();
    await quiz.getByRole("button", { name: /Doesn't matter/ }).click();
    await quiz.getByRole("button", { name: /Any era/ }).click();
    await quiz.getByRole("button", { name: "Next →" }).click();
    await quiz.getByRole("button", { name: /Either/ }).click();
    await expect(quiz.locator(".pk-result")).toHaveCount(3);
    await page.screenshot({ path: info.outputPath("phone-quiz.png") });
    const top = await quiz.locator(".pk-result h4").first().innerText();
    await quiz.locator(".pk-result").first().getByRole("button", { name: "Not interested" }).click();
    await expect(quiz.locator(".pk-result")).toHaveCount(3);
    await expect(quiz.locator(".pk-result h4").first()).not.toHaveText(top);
  });

  test("Back from a pick's page returns to the same picks", async ({ page }) => {
    await page.goto(`/library/${MOVIES_LIB}?tv=0`);
    await page.getByRole("button", { name: "Help me choose" }).click();
    const quiz = page.getByRole("dialog", { name: "Help me choose" });
    await quiz.getByRole("button", { name: /Stressed/ }).click();
    await quiz.getByRole("button", { name: /Change my mood/ }).click();
    await quiz.getByRole("button", { name: /Just me/ }).click();
    await quiz.getByRole("button", { name: /Doesn't matter/ }).click();
    await quiz.getByRole("button", { name: /Any era/ }).click();
    await quiz.getByRole("button", { name: "Next →" }).click();
    await quiz.getByRole("button", { name: /Either/ }).click();
    await expect(quiz.locator(".pk-result")).toHaveCount(3);
    const before = await quiz.locator(".pk-result h4").allInnerTexts();
    await quiz.locator(".pk-result h4 a").nth(1).click();
    await expect(page).toHaveURL(/\/item\/\d+/);
    await expect(page.locator(".pk-mood")).toBeVisible();
    await page.goBack();
    await expect(quiz.locator(".pk-result")).toHaveCount(3);
    expect(await quiz.locator(".pk-result h4").allInnerTexts()).toEqual(before);
    // Closing it leaves the library; a reload doesn't bring it back.
    await page.keyboard.press("Escape");
    await expect(quiz).toBeHidden();
    await page.reload();
    await expect(page.getByRole("button", { name: "Help me choose" })).toBeVisible();
    await expect(quiz).toBeHidden();

    // Surprise me: Pick another comes first and is ready under Enter.
    await page.getByRole("button", { name: "Surprise me" }).click();
    const sheet = page.getByRole("dialog", { name: "Surprise me" });
    await expect(sheet.locator(".pk-actions").first().locator("button").first()).toHaveText(/Pick another/);
    await expect(sheet.getByRole("button", { name: "Pick another" })).toBeFocused();
    const one = await sheet.locator(".pk-pick h3").innerText();
    await page.keyboard.press("Enter");
    await expect(sheet.locator(".pk-pick h3")).not.toHaveText(one);
    await expect(sheet.getByRole("button", { name: "Pick another" })).toBeFocused();
    const film = await sheet.locator(".pk-pick h3").innerText();
    await sheet.locator(".pk-pick h3 a").click();
    await expect(page).toHaveURL(/\/item\/\d+/);
    await page.goBack();
    await expect(sheet.locator(".pk-pick h3")).toHaveText(film);
  });

  test("a film's page shows its mood profile; a show library has no pick buttons", async ({ page }) => {
    await page.goto(`/library/${SHOWS_LIB}?tv=0`);
    await expect(page.getByRole("button", { name: /^Filters/ })).toBeVisible();
    await expect(page.getByRole("button", { name: "Surprise me" })).toHaveCount(0);
    await page.goto(`/library/${MOVIES_LIB}?tv=0`);
    await page.locator(".poster-card").first().click();
    await expect(page.locator(".pk-mood")).toBeVisible();
    await page.getByRole("button", { name: "More like this" }).click();
    await expect(page.getByRole("button", { name: "More like this" })).toHaveAttribute("aria-pressed", "true");
  });
});

test("Surprise me: each film appears with its poster already there", async ({ page, openTv }) => {
  // Every picture takes most of a second, as a poster the server has yet to fetch does.
  await page.route("**/art/**", async (route) => {
    await new Promise((r) => setTimeout(r, 700));
    return route.fallback();
  });
  await openTv(`/library/${MOVIES_LIB}`);
  await waitForFocus(page, /^c\d+$/);
  // On every frame: is there a film on screen whose poster isn't?
  await page.evaluate(() => {
    const w = window as unknown as { bare: string[] };
    w.bare = [];
    const look = () => {
      const pick = document.querySelector(".tv-pick");
      const title = pick?.querySelector(".tv-detail-title")?.textContent;
      const img = pick?.querySelector<HTMLImageElement>(".tv-pick-poster img");
      if (title && !(img && img.classList.contains("in") && img.complete && img.naturalWidth > 0) && !w.bare.includes(title)) w.bare.push(title);
      requestAnimationFrame(look);
    };
    requestAnimationFrame(look);
  });
  await page.locator('[data-fk="surprise"]').focus();
  await page.keyboard.press("Enter");
  await waitForFocus(page, "pk-next");
  const title = page.locator(".tv-pick .tv-detail-title");
  for (let i = 0; i < 4; i++) {
    const was = await title.innerText();
    // Long enough to read a title, as someone flicking through does.
    await page.waitForTimeout(1000);
    await page.keyboard.press("Enter");
    await expect(title).not.toHaveText(was);
  }
  await page.waitForTimeout(300);
  expect(await page.evaluate(() => (window as unknown as { bare: string[] }).bare), "films shown before their poster").toEqual([]);
});
