// Helpers for driving and checking the TV UI's D-pad focus (src/tv/focus.ts).
import { expect, type Page, type TestInfo } from "@playwright/test";

export type Dir = "up" | "down" | "left" | "right";
export const KEY: Record<Dir, string> = { up: "ArrowUp", down: "ArrowDown", left: "ArrowLeft", right: "ArrowRight" };

export interface Focused {
  /** data-fk of the focused element, or "" when it has none. */
  fk: string;
  tag: string;
  text: string;
  /** True when focus is on a D-pad focusable ([data-f]), not <body> or something stray. */
  ok: boolean;
  inNav: boolean;
  /** Fraction of the element that is actually on screen (viewport and clipping parents). */
  onScreen: number;
}

/** Runs in the page: what has focus, and how much of it can be seen. */
async function describeFocus(): Promise<Focused> {
  const el = document.activeElement as HTMLElement | null;
  if (!el || el === document.body) return { fk: "", tag: "body", text: "", ok: false, inNav: false, onScreen: 0 };
  // An IntersectionObserver accounts for the viewport and every clipping parent.
  const onScreen = await new Promise<number>((resolve) => {
    const io = new IntersectionObserver((entries) => {
      io.disconnect();
      resolve(entries[0]?.intersectionRatio ?? 0);
    });
    io.observe(el);
  });
  return {
    fk: el.dataset.fk || "",
    tag: el.tagName.toLowerCase(),
    text: (el.innerText || el.getAttribute("placeholder") || "").replace(/\s+/g, " ").trim().slice(0, 60),
    ok: el.hasAttribute("data-f"),
    inNav: !!el.closest(".tv-nav"),
    onScreen,
  };
}

export const focused = (page: Page) => page.evaluate(describeFocus);

/** Presses an arrow (or any key) and returns where focus ended up. */
export async function press(page: Page, key: Dir | string) {
  await page.keyboard.press(KEY[key as Dir] || key);
  return focused(page);
}

/** Waits until a D-pad focusable has focus (pages focus their first item after loading). */
export async function waitForFocus(page: Page, fk?: string | RegExp) {
  await expect
    .poll(async () => {
      const f = await focused(page);
      if (!f.ok) return false;
      if (fk === undefined) return true;
      return typeof fk === "string" ? f.fk === fk : fk.test(f.fk);
    }, { timeout: 10_000, message: `waiting for focus${fk ? ` on ${fk}` : ""}` })
    .toBe(true);
  return focused(page);
}

/** Lets cross-fades and image loads finish, then saves a screenshot and attaches it to the report. */
export async function snap(page: Page, info: TestInfo, name: string) {
  await page.waitForTimeout(450);
  const body = await page.screenshot({ path: info.outputPath(`${name}.png`) });
  await info.attach(name, { body, contentType: "image/png" });
  // SCREENS_DIR=e2e/screens collects every screen in one folder to flick through.
  if (process.env.SCREENS_DIR) {
    const { mkdirSync, writeFileSync } = await import("node:fs");
    const { join } = await import("node:path");
    mkdirSync(process.env.SCREENS_DIR, { recursive: true });
    writeFileSync(join(process.env.SCREENS_DIR, `${info.titlePath.slice(1).join(" - ").replace(/[^\w -]+/g, "")} - ${name}.png`), body);
  }
}

/**
 * Presses `dir` until focus stops moving (or `max` presses), failing if focus
 * is ever lost or lands somewhere you can't see. Returns the path taken.
 */
export async function walk(page: Page, dir: Dir, max = 40) {
  const path: Focused[] = [await focused(page)];
  for (let i = 0; i < max; i++) {
    const f = await press(page, dir);
    expect(f.ok, `focus lost after ${KEY[dir]} #${i + 1} (on ${f.tag})`).toBe(true);
    expect(f.onScreen, `focused "${f.text || f.fk}" is off screen after ${KEY[dir]}`).toBeGreaterThan(0.5);
    const prev = path[path.length - 1];
    if (f.fk === prev.fk && f.text === prev.text) break;
    path.push(f);
  }
  return path;
}

export interface AuditProblem {
  from: string;
  key: string;
  problem: string;
}

export interface AuditResult {
  checked: number;
  focusables: number;
  problems: AuditProblem[];
  unreachable: string[];
}

/**
 * Exhaustive check of the screen that's showing: from every focusable, press
 * each arrow and check that
 *  - focus never ends up on <body> or a non-focusable,
 *  - if anything focusable lies entirely in that direction, focus moves
 *    (the "Down doesn't leave the hero" class of bug), and moves that way,
 *  - wherever focus lands is actually visible on screen,
 * then that everything on the page can be reached from the starting point.
 * `limit` caps how many focusables are checked (long grids).
 */
export async function auditFocus(page: Page, opts: { limit?: number; skip?: (fk: string) => boolean } = {}): Promise<AuditResult> {
  // Instant layout while auditing: no transitions shifting things mid-check.
  const style = await page.addStyleTag({ content: "*,*::before,*::after{transition:none!important;animation:none!important}" });
  const start = await focused(page);

  const count = await page.evaluate(() => {
    const scopes = document.querySelectorAll("[data-scope]");
    const root: ParentNode = scopes.length ? scopes[scopes.length - 1] : document;
    document.querySelectorAll("[data-e2e]").forEach((e) => e.removeAttribute("data-e2e"));
    const els = [...root.querySelectorAll<HTMLElement>("[data-f]")].filter((el) => {
      const r = el.getBoundingClientRect();
      return !(el as HTMLButtonElement).disabled && r.width > 0 && r.height > 0;
    });
    els.forEach((el, i) => el.setAttribute("data-e2e", String(i)));
    return els.length;
  });

  const label = (i: number) =>
    page.evaluate((i) => {
      const el = document.querySelector<HTMLElement>(`[data-e2e="${i}"]`);
      return el ? `${el.dataset.fk || el.tagName.toLowerCase()}${el.innerText ? ` "${el.innerText.replace(/\s+/g, " ").trim().slice(0, 30)}"` : ""}` : `#${i}`;
    }, i);

  const problems: AuditProblem[] = [];
  const edges = new Map<number, Set<number>>();
  const startIdx = await page.evaluate(() => Number((document.activeElement as HTMLElement | null)?.dataset.e2e ?? -1));
  const n = opts.limit ? Math.min(count, opts.limit) : count;
  let checked = 0;

  for (let i = 0; i < n; i++) {
    const name = await label(i);
    if (opts.skip?.(name)) continue;
    for (const dir of ["up", "down", "left", "right"] as Dir[]) {
      // Focus the element the way the engine would, then measure the layout.
      const before = await page.evaluate(
        async ({ i, dir }) => {
          const el = document.querySelector<HTMLElement>(`[data-e2e="${i}"]`);
          if (!el) return null;
          // Arrive the way the engine would (scrolling the row/page into place). The dev
          // server shares the module with the app; a built bundle falls back to plain focus.
          const url = "/src/tv/focus.ts";
          const engine: { focusEl?: (el: HTMLElement, instant?: boolean) => boolean } | null = await import(/* @vite-ignore */ url).catch(() => null);
          if (engine?.focusEl) engine.focusEl(el, true);
          else el.focus({ preventScroll: true });
          const a = el.getBoundingClientRect();
          const overlapsY = (r: DOMRect) => Math.min(a.bottom, r.bottom) - Math.max(a.top, r.top) > 0;
          const home = el.closest<HTMLElement>("[data-enter]");
          const exit = home && ({ left: "right", right: "left", up: "down", down: "up" } as Record<string, string>)[home.dataset.enter!];
          const ahead: number[] = [];
          const rects: Record<number, { cx: number; cy: number; top: number; bottom: number; group: string }> = {};
          for (const o of document.querySelectorAll<HTMLElement>("[data-e2e]")) {
            const j = Number(o.dataset.e2e);
            const r = o.getBoundingClientRect();
            rects[j] = { cx: (r.left + r.right) / 2, cy: (r.top + r.bottom) / 2, top: r.top, bottom: r.bottom, group: o.closest<HTMLElement>("[data-group]")?.dataset.group || "" };
            if (o === el || el.contains(o)) continue;
            // Groups with data-enter (the sidebar) are entered one way, left the opposite way.
            const g: HTMLElement | null = o.closest<HTMLElement>("[data-enter]");
            if (g && !g.contains(el) && g.dataset.enter !== dir) continue;
            if (home && dir !== exit && !home.contains(o)) continue;
            const t = 2;
            const isAhead =
              dir === "down" ? r.top >= a.bottom - t : dir === "up" ? r.bottom <= a.top + t : dir === "right" ? r.left >= a.right - t : r.right <= a.left + t;
            // Sideways only counts on the same line (or into a group alongside, like the sidebar):
            // the end of a row is meant to stop there.
            const sameLine = overlapsY(r) || overlapsY((o.closest("[data-group]") || o).getBoundingClientRect());
            if (isAhead && (dir === "up" || dir === "down" || sameLine)) ahead.push(j);
          }
          return { ahead, rects };
        },
        { i, dir },
      );
      if (!before) continue;
      checked++;
      await page.keyboard.press(KEY[dir]);
      const after = await page.evaluate(() => {
        const el = document.activeElement as HTMLElement | null;
        return { idx: el?.dataset.e2e != null ? Number(el.dataset.e2e) : -1, tag: el?.tagName.toLowerCase() || "none", f: !!el?.hasAttribute("data-f") };
      });
      const where = await focused(page);
      const key = KEY[dir];
      if (!after.f) {
        problems.push({ from: name, key, problem: `focus lost (now on <${after.tag}>)` });
      } else if (after.idx === i) {
        if (before.ahead.length) problems.push({ from: name, key, problem: `stuck: ${before.ahead.length} item(s) that way, e.g. ${await label(before.ahead[0])}` });
      } else {
        if (after.idx >= 0) {
          if (!edges.has(i)) edges.set(i, new Set());
          edges.get(i)!.add(after.idx);
          const a = before.rects[i], b = before.rects[after.idx];
          const wrongWay = dir === "down" ? b.cy <= a.cy : dir === "up" ? b.cy >= a.cy : dir === "right" ? b.cx <= a.cx : b.cx >= a.cx;
          if (wrongWay) problems.push({ from: name, key, problem: `moved the wrong way, to ${await label(after.idx)}` });
          // Up/Down must not jump over a whole row of focusables to a farther one.
          if (!wrongWay && (dir === "up" || dir === "down")) {
            const skipped = before.ahead.find((j) => {
              const c = before.rects[j];
              if (c.group && c.group === b.group) return false;
              return dir === "down" ? c.bottom <= b.top : c.top >= b.bottom;
            });
            if (skipped !== undefined) problems.push({ from: name, key, problem: `skipped over ${await label(skipped)} to ${await label(after.idx)}` });
          }
        }
        if (where.onScreen < 0.5) problems.push({ from: name, key, problem: `landed off screen on ${await label(after.idx)} (${Math.round(where.onScreen * 100)}% visible)` });
      }
    }
  }

  // Reachability from where the page put focus.
  let unreachable: string[] = [];
  if (!opts.limit && startIdx >= 0) {
    const seen = new Set([startIdx]);
    const queue = [startIdx];
    while (queue.length) for (const j of edges.get(queue.shift()!) || []) if (!seen.has(j)) (seen.add(j), queue.push(j));
    for (let i = 0; i < count; i++) if (!seen.has(i)) unreachable.push(await label(i));
    if (opts.skip) unreachable = unreachable.filter((u) => !opts.skip!(u));
  }

  // Put things back as they were.
  await page.evaluate(() => document.querySelectorAll("[data-e2e]").forEach((e) => e.removeAttribute("data-e2e")));
  await style.evaluate((s) => (s as HTMLElement).remove());
  if (start.fk) await page.evaluate((fk) => document.querySelector<HTMLElement>(`[data-fk="${CSS.escape(fk)}"]`)?.focus(), start.fk);
  return { checked, focusables: count, problems, unreachable };
}

/** Fails the test with a readable list if the audit found anything. */
export function expectClean(r: AuditResult, info: TestInfo, name: string) {
  const report = [
    `${name}: ${r.focusables} focusables, ${r.checked} key presses checked`,
    ...r.problems.map((p) => `  ${p.key.padEnd(10)} from ${p.from}: ${p.problem}`),
    ...(r.unreachable.length ? [`  unreachable from the starting focus: ${r.unreachable.join(", ")}`] : []),
  ].join("\n");
  info.attach(`${name} focus audit`, { body: report, contentType: "text/plain" });
  expect(r.problems.length + r.unreachable.length, report).toBe(0);
}
