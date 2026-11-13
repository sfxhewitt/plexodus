// The A–Z jump bar on a library sorted by title.
import { expect, test } from "./fixtures";
import { focused, press, snap, waitForFocus } from "./focus";
import { MOVIES_LIB } from "./mock";

test.skip(!!process.env.E2E_BASE_URL, "uses the mock library");

test.afterEach(({ unhandled }) => {
  expect(unhandled, "API calls the mock doesn't answer (add them to e2e/mock.ts)").toEqual([]);
});

test("A–Z bar: Right from the grid's edge, pick a letter, land on its first title", async ({ page, openTv }, info) => {
  await openTv(`/library/${MOVIES_LIB}`);
  await waitForFocus(page, /^c\d+$/);
  const letters = page.locator(".tv-az-letter");
  expect(await letters.count()).toBeGreaterThan(3);
  // Along the first row to the bar.
  let f = await focused(page);
  for (let i = 0; i < 12 && !f.fk?.startsWith("az-"); i++) f = await press(page, "right");
  expect(f.fk, "Right from the last poster reaches the A–Z bar").toMatch(/^az-/);
  // Down the bar to the last letter, then OK.
  for (let i = 0; i < 30; i++) {
    const n = await press(page, "down");
    if (!n.fk?.startsWith("az-") || n.fk === f.fk) break;
    f = n;
  }
  if (!(await focused(page)).fk?.startsWith("az-")) await press(page, "up");
  expect((await focused(page)).fk).toBe(f.fk);
  const want = f.fk!.slice(3);
  await snap(page, info, "01 A–Z bar");
  await page.keyboard.press("Enter");
  await expect.poll(async () => (await focused(page)).fk ?? "").toMatch(/^c\d+$/);
  const title = await page.locator(":focus .tv-card-cap .t, :focus").first().innerText();
  expect(title.replace(/^(the|a|an) /i, "")[0].toUpperCase()).toBe(want);
  await expect(page.locator(`.tv-az-letter.on[data-fk="az-${want}"]`)).toHaveCount(1);
  await snap(page, info, "02 jumped");
});
