// "Who's watching?" on the TV: pick a profile, PIN pad, back to the main profile, edit a profile.
import { expect, test } from "./fixtures";
import { auditFocus, expectClean, press, snap, waitForFocus, walk } from "./focus";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");
test.use({ profiles: true });

test.afterEach(({ unhandled }) => {
  expect(unhandled, "API calls the mock doesn't answer (add them to e2e/mock.ts)").toEqual([]);
});

test("who's watching: kids profile, then PIN back to the main profile", async ({ page, openTv }, info) => {
  await openTv("/");
  let f = await waitForFocus(page, "pf-1");
  await snap(page, info, "01 who's watching");
  const along = await walk(page, "right");
  expect(along.map((x) => x.fk)).toEqual(["pf-1", "pf-2", "pf-3"]);
  expect((await press(page, "down")).fk, "Down reaches Manage profiles").toBe("pf-manage");
  expect((await press(page, "up")).fk, "Up returns to the last profile").toBe("pf-3");

  // Main profile -> kids profile needs no PIN.
  f = await press(page, "left");
  expect(f.fk).toBe("pf-2");
  await page.keyboard.press("Enter");
  f = await waitForFocus(page, "hero-play");
  await expect(page.locator(".tv-avatar")).toContainText("🦊");

  // Settings: switch profile -> main profile asks for its PIN.
  await page.goto("/settings?tv=1");
  // A reload keeps the profile picked for this app session.
  await waitForFocus(page, "profiles");
  await expect(page.getByRole("button", { name: "Manage profiles" }), "a kids profile can't manage profiles").toHaveCount(0);
  await page.keyboard.press("Enter");
  await waitForFocus(page, "pf-2");
  await expect(page.getByRole("button", { name: "Manage profiles" })).toHaveCount(0);
  await press(page, "left");
  await page.keyboard.press("Enter");
  f = await waitForFocus(page, "pin-5");
  await snap(page, info, "02 pin pad");
  // Wrong PIN with the remote's number keys, then the right one on the pad.
  for (const k of "0000") await page.keyboard.press(k);
  await expect(page.locator(".tv-pf-pin .tv-error")).toHaveText("wrong PIN");
  await expect(page.locator(".tv-pf-dots .on")).toHaveCount(0);
  expectClean(await auditFocus(page), info, "pin pad");
  for (const k of ["1", "2", "3", "4"]) {
    await page.locator(`[data-fk="pin-${k}"]`).focus();
    await page.keyboard.press("Enter");
  }
  await waitForFocus(page, "hero-play");
  await expect(page.locator(".tv-avatar")).toContainText("D");
});

test("manage profiles: edit a profile with the D-pad", async ({ page, openTv }, info) => {
  await openTv("/");
  await waitForFocus(page, "pf-1");
  // Into manage mode, then edit Mia.
  await press(page, "down");
  await page.keyboard.press("Enter");
  await expect(page.locator(".tv-h1")).toHaveText("Manage profiles");
  await press(page, "up");
  await waitForFocus(page, /^pf-/);
  await page.locator('[data-fk="pf-2"]').focus();
  await page.keyboard.press("Enter");
  await waitForFocus(page, "save");
  await snap(page, info, "01 editor");
  expectClean(await auditFocus(page, { skip: (fk) => fk === "name" }), info, "editor");

  // Up from Save walks through PIN, kids levels, colours and pictures to the name.
  await page.locator('[data-fk="save"]').focus();
  const up = await walk(page, "up");
  expect(up.at(-1)!.fk, "Up ends on the name").toBe("name");
  expect(up.some((x) => x.fk.startsWith("kids-")), "passes the kids levels").toBe(true);
  // Pick "Teens" and save.
  await page.locator('[data-fk="kids-14"]').focus();
  await page.keyboard.press("Enter");
  await page.locator('[data-fk="save"]').focus();
  await page.keyboard.press("Enter");
  await waitForFocus(page, "pf-2");
  await expect(page.locator(".tv-pf-tile").nth(1)).toContainText("KIDS");
  // Back leaves manage mode... and the picker still has focus.
  await page.keyboard.press("Escape");
  await expect(page.locator(".tv-h1")).toHaveText("Who's watching?");
  await snap(page, info, "02 after edit");
});
