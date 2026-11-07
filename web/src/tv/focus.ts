// Spatial navigation for the TV interface.
//
// Anything with a `data-f` attribute can take focus. Arrow keys move to the
// nearest focusable in that direction, row first: down goes to the next row
// and then to the item closest horizontally, like Android TV's Leanback.
//
// - `data-group="key"` marks a set of items (a row, the sidebar). Coming back
//   into a group lands on the item you last left there, not the nearest one.
//   `data-default` marks the item to land on the first time.
// - `data-scope` marks a modal; only focusables inside the last one count.
// - `data-hscroll` on a row's track keeps the focused card pinned to its left
//   (`data-hscroll="reveal"`: only scrolls enough to show it).
// - `data-vscroll="pin|reveal|center"` on a vertical scroller keeps the focused
//   row (closest `data-row`, else the item) in place.
// - `data-fk` gives an item a stable key so focus survives re-renders and is
//   restored when you come back to a page.

type Dir = "up" | "down" | "left" | "right";
const OPPOSITE: Record<Dir, Dir> = { up: "down", down: "up", left: "right", right: "left" };

const lastInGroup = new Map<string, string>();

function visible(el: HTMLElement) {
  if ((el as HTMLButtonElement).disabled) return false;
  const r = el.getBoundingClientRect();
  return r.width > 0 && r.height > 0;
}

function scopeRoot(): ParentNode {
  const scopes = document.querySelectorAll("[data-scope]");
  return scopes.length ? scopes[scopes.length - 1] : document;
}

export function focusables(root: ParentNode = scopeRoot()) {
  return [...root.querySelectorAll<HTMLElement>("[data-f]")].filter(visible);
}

const groupOf = (el: Element | null) => el?.closest<HTMLElement>("[data-group]")?.dataset.group;

function pick(from: HTMLElement, dir: Dir, cands: HTMLElement[]): HTMLElement | undefined {
  const a = from.getBoundingClientRect();
  const acx = (a.left + a.right) / 2;
  const acy = (a.top + a.bottom) / 2;
  const rects = cands.filter((el) => el !== from && !from.contains(el)).map((el) => ({ el, r: el.getBoundingClientRect() }));

  if (dir === "up" || dir === "down") {
    const ahead = rects.filter(({ r }) => {
      const cy = (r.top + r.bottom) / 2;
      return dir === "down" ? cy > acy + 2 && r.bottom > a.bottom - 1 : cy < acy - 2 && r.top < a.top + 1;
    });
    if (!ahead.length) return;
    // Nearest row first...
    const gap = (r: DOMRect) => Math.max(0, dir === "down" ? r.top - a.bottom : a.top - r.bottom);
    const near = Math.min(...ahead.map(({ r }) => gap(r)));
    // The band is sized by the nearest item, so a row of tall cards just below
    // a short row (season tabs) isn't pulled in and skipped to.
    const nearest = ahead.find(({ r }) => gap(r) === near)!;
    const band = ahead.filter(({ r }) => gap(r) <= near + Math.max(8, nearest.r.height * 0.5));
    // ...then the closest item in it horizontally.
    let best = band[0];
    let bestD = Infinity;
    for (const c of band) {
      const overlap = Math.min(a.right, c.r.right) - Math.max(a.left, c.r.left);
      const d = overlap > 0 ? -overlap / 1e4 : Math.abs((c.r.left + c.r.right) / 2 - acx);
      if (d < bestD) {
        bestD = d;
        best = c;
      }
    }
    return best.el;
  }

  const sideways = rects.filter(({ r }) => {
    const cx = (r.left + r.right) / 2;
    return dir === "right" ? cx > acx + 2 && r.right > a.right - 1 : cx < acx - 2 && r.left < a.left + 1;
  });
  // Stay on the same line. Only jump to another line when the target's whole
  // group spans this one (the sidebar), never to the next row at a row's end.
  const overlapsY = (r: DOMRect) => Math.min(a.bottom, r.bottom) - Math.max(a.top, r.top) > 0;
  const ahead = sideways.filter(({ el, r }) => overlapsY(r) || overlapsY((el.closest("[data-group]") || el).getBoundingClientRect()));
  if (!ahead.length) return;
  let best: (typeof ahead)[number] | undefined;
  let bestScore = Infinity;
  for (const c of ahead) {
    const overlapY = Math.min(a.bottom, c.r.bottom) - Math.max(a.top, c.r.top);
    const dx = Math.max(0, dir === "right" ? c.r.left - a.right : a.left - c.r.right);
    const dy = overlapY > 0 ? 0 : Math.abs((c.r.top + c.r.bottom) / 2 - acy);
    // Strongly prefer things on the same line.
    const score = dx + dy * 4 + (overlapY > 0 ? 0 : 2000);
    if (score < bestScore) {
      bestScore = score;
      best = c;
    }
  }
  return best?.el;
}

/** If `target` is in a group we're entering, prefer that group's remembered or default item. */
function enterGroup(from: HTMLElement | null, target: HTMLElement, all: HTMLElement[]) {
  const g = groupOf(target);
  if (!g || g === groupOf(from)) return target;
  const groupEl = target.closest<HTMLElement>("[data-group]")!;
  if (groupEl.dataset.remember !== "false") {
    const key = lastInGroup.get(g);
    const remembered = key ? all.find((el) => el.dataset.fk === key && groupOf(el) === g) : undefined;
    if (remembered) return remembered;
  }
  const def = all.find((el) => el.hasAttribute("data-default") && groupOf(el) === g);
  return def || target;
}

export function focusEl(el: HTMLElement | null | undefined, instant = false) {
  if (!el) return false;
  el.focus({ preventScroll: true });
  scrollIntoPlace(el, instant);
  return true;
}

export function move(dir: Dir, instant = false) {
  const all = focusables();
  const cur = document.activeElement as HTMLElement | null;
  const inside = cur && cur !== document.body && all.includes(cur) ? cur : null;
  if (!inside) return focusEl(initialTarget(all), true);
  // Some groups (the sidebar) can only be entered from one side, e.g. Left,
  // and only left the opposite way: inside, the other arrows stay in the group.
  const home = inside.closest<HTMLElement>("[data-enter]");
  const cands = home && dir !== OPPOSITE[home.dataset.enter as Dir]
    ? all.filter((el) => home.contains(el))
    : all.filter((el) => {
        const g = el.closest<HTMLElement>("[data-enter]");
        return !g || g.contains(inside) || g.dataset.enter === dir;
      });
  // Groups entered from one side (the tab bar) are a last resort: Up only
  // reaches the tab bar from the topmost thing on the page, even if the page
  // is scrolled and what's above you is off screen.
  const plain = home ? cands : cands.filter((el) => !el.closest("[data-enter]"));
  const t = pick(inside, dir, plain) || pick(inside, dir, cands);
  if (!t) return false;
  return focusEl(enterGroup(inside, t, all), instant);
}

function initialTarget(all = focusables()) {
  return all.find((el) => el.hasAttribute("data-autofocus")) || all[0];
}

/** Focus a page's starting point (or the item with key `fk`) once it has rendered. */
export function focusInitial(fk?: string | null) {
  const all = focusables();
  const el = (fk && all.find((e) => e.dataset.fk === fk)) || initialTarget(all);
  return focusEl(el, true);
}

export function focusKey(fk: string) {
  return focusEl(focusables().find((e) => e.dataset.fk === fk), true);
}

function remPx(v: string | undefined) {
  if (!v) return 0;
  const n = parseFloat(v);
  return v.endsWith("rem") ? n * parseFloat(getComputedStyle(document.documentElement).fontSize) : n;
}

// ---- scrolling ----
//
// Rows and pages scroll with our own short ease-out instead of the browser's
// smooth scroll, which takes up to ~0.6 s per move in Android WebViews and
// queues up behind fast presses. The scrollers are overflow:scroll (with the
// scrollbar hidden) so the WebView composites them: moving one is a cheap
// offset change, not a repaint of every card in it.

const SCROLL_MS = 180;
const anims = new Map<HTMLElement, { raf: number; axis: "left" | "top"; to: number }>();
const instantScroll = () =>
  (window as unknown as { __plexodusInstantScroll?: boolean }).__plexodusInstantScroll === true ||
  window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;

/** Where `sc` is heading (the end of a running animation), else where it is. */
function scrollPos(sc: HTMLElement, axis: "left" | "top") {
  const a = anims.get(sc);
  if (a && a.axis === axis) return a.to;
  return axis === "left" ? sc.scrollLeft : sc.scrollTop;
}

function scrollToPos(sc: HTMLElement, axis: "left" | "top", to: number, instant: boolean) {
  const prev = anims.get(sc);
  if (prev) cancelAnimationFrame(prev.raf);
  anims.delete(sc);
  const set = (v: number) => (axis === "left" ? (sc.scrollLeft = v) : (sc.scrollTop = v));
  const from = axis === "left" ? sc.scrollLeft : sc.scrollTop;
  if (instant || instantScroll() || Math.abs(to - from) < 2) {
    set(to);
    return;
  }
  const t0 = performance.now();
  const a = { raf: 0, axis, to };
  const step = (t: number) => {
    const k = Math.min(1, (t - t0) / SCROLL_MS);
    const e = 1 - Math.pow(1 - k, 3); // ease-out cubic
    set(from + (to - from) * e);
    if (k < 1) a.raf = requestAnimationFrame(step);
    else anims.delete(sc);
  };
  a.raf = requestAnimationFrame(step);
  anims.set(sc, a);
}

function scrollIntoPlace(el: HTMLElement, instant: boolean) {

  const track = el.closest<HTMLElement>("[data-hscroll]");
  if (track) {
    const tr = track.getBoundingClientRect();
    const er = el.getBoundingClientRect();
    const pad = parseFloat(getComputedStyle(track).paddingLeft) || 0;
    // Measure against where the track is heading, so quick presses during an
    // animation still land each card in place.
    const cur = scrollPos(track, "left");
    const shift = cur - track.scrollLeft;
    let left = cur + (er.left - shift - tr.left) - pad;
    if (track.dataset.hscroll === "reveal") {
      // Only scroll as far as needed to show it (the tab bar).
      left = cur;
      if (er.right - shift + pad > tr.right) left += er.right - shift + pad - tr.right;
      if (er.left - shift - pad < tr.left) left -= tr.left - (er.left - shift - pad);
    }
    left = Math.max(0, Math.min(left, track.scrollWidth - track.clientWidth));
    if (Math.abs(left - cur) > 1) scrollToPos(track, "left", left, instant);
  }

  const sc = el.closest<HTMLElement>("[data-vscroll]");
  if (sc) {
    const mode = sc.dataset.vscroll;
    const row = (el.closest<HTMLElement>("[data-row]") as HTMLElement | null) || el;
    const sr = sc.getBoundingClientRect();
    const rr = row.getBoundingClientRect();
    const cur = scrollPos(sc, "top");
    const shift = cur - sc.scrollTop;
    const top = cur + rr.top - shift - sr.top;
    let target = cur;
    if (row.hasAttribute("data-row-top")) target = 0;
    else if (mode === "pin") target = top - remPx(sc.dataset.pin);
    else if (mode === "center") target = top - (sc.clientHeight - rr.height) / 2;
    else {
      const m = remPx(sc.dataset.margin || "3rem");
      if (rr.bottom - shift + m > sr.bottom) target = top + rr.height + m - sc.clientHeight;
      if (rr.top - shift - m < sr.top) target = top - m;
    }
    target = Math.max(0, Math.min(target, sc.scrollHeight - sc.clientHeight));
    if (Math.abs(target - cur) > 1) scrollToPos(sc, "top", target, instant);
  }
}

// ---- Back button ----

type BackHandler = () => boolean;
const backStack: BackHandler[] = [];

/** Register a handler for Back; the most recent one that returns true wins. */
export function onBack(h: BackHandler) {
  backStack.push(h);
  return () => {
    const i = backStack.lastIndexOf(h);
    if (i >= 0) backStack.splice(i, 1);
  };
}

/** Runs the Back handlers. Returns "handled" or "exit" (the app should close). */
export function handleBack(): "handled" | "exit" {
  for (let i = backStack.length - 1; i >= 0; i--) if (backStack[i]()) return "handled";
  return "exit";
}

// ---- Global key handling ----

// After an action removes the focused card (Remove from Up Next),
// focus goes to one of these neighbours instead of the top of the page.
let fallback: { fks: string[]; until: number } | null = null;
export function setFocusFallback(fks: string[]) {
  fallback = fks.length ? { fks, until: performance.now() + 5000 } : null;
  // A removed element doesn't always fire focusout: keep an eye out meanwhile.
  const t = setInterval(() => {
    recoverFocus();
    if (!fallback) clearInterval(t);
  }, 100);
  setTimeout(() => clearInterval(t), 5000);
}

/** If focus fell to <body>, put it on the fallback neighbour or the page's start. */
function recoverFocus() {
  const a = document.activeElement as HTMLElement | null;
  if (a && a !== document.body && a.isConnected) return;
  if (!document.hasFocus()) return;
  const all = focusables();
  const near = fallback && performance.now() < fallback.until
    ? fallback.fks.map((k) => all.find((el) => el.dataset.fk === k)).find(Boolean)
    : undefined;
  fallback = null;
  focusEl(near || initialTarget(all), true);
}

// After a long press opens a menu, OK is still held: its repeats and its
// release must not press whatever the menu focused.
let swallowSelect = false;
export function swallowSelectUntilRelease() {
  swallowSelect = true;
}

export function installKeys() {
  const isSelectKey = (e: KeyboardEvent) => e.key === "Enter" || e.keyCode === 23;
  window.addEventListener("keydown", (e) => {
    if (swallowSelect && isSelectKey(e)) {
      e.preventDefault();
      e.stopPropagation();
    }
  }, true);
  window.addEventListener("keyup", (e) => {
    if (swallowSelect && isSelectKey(e)) {
      swallowSelect = false;
      e.preventDefault();
      e.stopPropagation();
    }
  }, true);

  // The Android app calls this when the remote's Back is pressed.
  (window as unknown as { __plexodusBack?: () => string }).__plexodusBack = handleBack;

  window.addEventListener("keydown", (e) => {
    const dir = ({ ArrowUp: "up", ArrowDown: "down", ArrowLeft: "left", ArrowRight: "right" } as const)[e.key as string];
    const t = e.target as HTMLElement;
    const typing = t.tagName === "INPUT" || t.tagName === "TEXTAREA";
    if (dir) {
      if (typing) {
        const inp = t as HTMLInputElement;
        const atStart = inp.selectionStart === 0 && inp.selectionEnd === 0;
        const atEnd = inp.selectionStart === inp.value.length;
        if ((dir === "left" && !atStart) || (dir === "right" && !atEnd)) return;
      }
      e.preventDefault();
      // Held-down keys repeat fast; skip the scroll animation so it keeps up.
      // Quick taps just retarget the running animation.
      move(dir, e.repeat);
      return;
    }
    if (e.key === "Enter" && (document.activeElement === document.body || !document.activeElement)) {
      e.preventDefault();
      focusEl(initialTarget(), true);
      return;
    }
    if ((e.key === "Escape" || e.key === "GoBack" || e.key === "BrowserBack" || (e.key === "Backspace" && !typing))) {
      e.preventDefault();
      if (handleBack() === "exit") (window as unknown as { PlexodusNative?: { exit?: () => void } }).PlexodusNative?.exit?.();
    }
  });

  document.addEventListener("focusin", (e) => {
    const el = e.target as HTMLElement;
    const g = groupOf(el);
    if (g && el.dataset.fk) lastInGroup.set(g, el.dataset.fk);
  });

  // Never leave focus stranded on <body> (e.g. after the focused card is removed).
  window.addEventListener("focusout", () => {
    setTimeout(recoverFocus, 50);
  });
}

export function rememberInGroup(group: string, fk: string) {
  lastInGroup.set(group, fk);
}
