import { test as base, expect } from "@playwright/test";
import { mockServer } from "./mock";
import { waitForFocus } from "./focus";

export const live = !!process.env.E2E_BASE_URL;

export const test = base.extend<{ unhandled: string[]; openTv: (path?: string) => Promise<void> }>({
  unhandled: async ({}, use) => {
    await use([]);
  },
  page: async ({ page, unhandled }, use) => {
    if (!live) await mockServer(page, { unhandled });
    await page.addInitScript(() => {
      // Same as a TV box: TV mode on. Scrolls land instantly so every check sees final positions.
      try {
        localStorage.setItem("plexodus.tv", "1");
      } catch {
        /* ignore */
      }
      (window as unknown as { __plexodusInstantScroll?: boolean }).__plexodusInstantScroll = true;
      const instant = <T extends (...a: never[]) => unknown>(orig: T) =>
        function (this: unknown, ...args: unknown[]) {
          const a = args[0];
          if (a && typeof a === "object") args[0] = { ...(a as object), behavior: "instant" };
          return (orig as unknown as (...a: unknown[]) => unknown).apply(this, args);
        };
      Element.prototype.scrollTo = instant(Element.prototype.scrollTo) as typeof Element.prototype.scrollTo;
      Element.prototype.scrollBy = instant(Element.prototype.scrollBy) as typeof Element.prototype.scrollBy;
    });
    const errors: string[] = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await use(page);
    expect(errors, "uncaught errors in the page").toEqual([]);
  },
  openTv: async ({ page }, use) => {
    await use(async (path = "/") => {
      await page.goto(`${path}${path.includes("?") ? "&" : "?"}tv=1`);
      if (live && process.env.E2E_USER) {
        const user = page.getByPlaceholder("Username");
        if (await user.isVisible({ timeout: 3000 }).catch(() => false)) {
          await user.fill(process.env.E2E_USER);
          await page.getByPlaceholder("Password").fill(process.env.E2E_PASS || "");
          await page.keyboard.press("Enter");
        }
      }
      await waitForFocus(page);
    });
  },
});

export { expect };
