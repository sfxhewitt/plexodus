// Exhaustive focus audit of every TV screen: from each focusable, each arrow.
import { test } from "./fixtures";
import { auditFocus, expectClean, snap, waitForFocus } from "./focus";
import { ids, MOVIES_LIB, SHOWS_LIB } from "./mock";

const screens: [string, string, { limit?: number }?][] = [
  ["home", "/"],
  ["movie", `/item/${ids.movie}`],
  ["show", `/item/${ids.show}`],
  ["season", `/item/${ids.season}`],
  ["episode", `/item/${ids.episode}`],
  ["movies library", `/library/${MOVIES_LIB}`],
  ["shows library", `/library/${SHOWS_LIB}`],
  ["search", "/search"],
  ["settings", "/settings"],
];

test.describe("focus audit", () => {
  test.skip(!!process.env.E2E_BASE_URL, "uses the mock library's ids");
  for (const [name, path, opts] of screens) {
    test(name, async ({ page, openTv }, info) => {
      await openTv(path);
      await snap(page, info, name);
      expectClean(await auditFocus(page, opts), info, name);
      await waitForFocus(page);
    });
  }
});
