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
// - `data-hscroll` on a row's track keeps the focused card pinned to its left.
// - `data-vscroll="pin|reveal|center"` on a vertical scroller keeps the focused
//   row (closest `data-row`, else the item) in place.
// - `data-fk` gives an item a stable key so focus survives re-renders and is
//   restored when you come back to a page.

type Dir = "up" | "down" | "left" | "right";

const lastInGroup = new Map<string, string>();
let lastMove = 0;

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
    const band = ahead.filter(({ r }) => gap(r) <= near + Math.max(8, r.height * 0.5));
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

  const ahead = rects.filter(({ r }) => {
    const cx = (r.left + r.right) / 2;
    return dir === "right" ? cx > acx + 2 && r.right > a.right - 1 : cx < acx - 2 && r.left < a.left + 1;
  });
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
  // Some groups (the sidebar) can only be entered from one side, e.g. Left.
  const cands = all.filter((el) => {
    const g = el.closest<HTMLElement>("[data-enter]");
    return !g || g.contains(inside) || g.dataset.enter === dir;
  });
  const t = pick(inside, dir, cands);
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

function scrollIntoPlace(el: HTMLElement, instant: boolean) {
  const behavior: ScrollBehavior = instant ? "auto" : "smooth";

  const track = el.closest<HTMLElement>("[data-hscroll]");
  if (track) {
    const tr = track.getBoundingClientRect();
    const er = el.getBoundingClientRect();
    const pad = parseFloat(getComputedStyle(track).paddingLeft) || 0;
    const left = track.scrollLeft + (er.left - tr.left) - pad;
    if (Math.abs(left - track.scrollLeft) > 1) track.scrollTo({ left, behavior });
  }

  const sc = el.closest<HTMLElement>("[data-vscroll]");
  if (sc) {
    const mode = sc.dataset.vscroll;
    const row = (el.closest<HTMLElement>("[data-row]") as HTMLElement | null) || el;
    const sr = sc.getBoundingClientRect();
    const rr = row.getBoundingClientRect();
    const top = sc.scrollTop + rr.top - sr.top;
    let target = sc.scrollTop;
    if (row.hasAttribute("data-row-top")) target = 0;
    else if (mode === "pin") target = top - remPx(sc.dataset.pin);
    else if (mode === "center") target = top - (sc.clientHeight - rr.height) / 2;
    else {
      const m = remPx(sc.dataset.margin || "3rem");
      if (rr.bottom + m > sr.bottom) target = top + rr.height + m - sc.clientHeight;
      if (rr.top - m < sr.top) target = top - m;
    }
    target = Math.max(0, Math.min(target, sc.scrollHeight - sc.clientHeight));
    if (Math.abs(target - sc.scrollTop) > 1) sc.scrollTo({ top: target, behavior });
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

export function installKeys() {
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
      const now = performance.now();
      // Held-down keys repeat fast; skip the smooth scroll so it keeps up.
      const instant = e.repeat || now - lastMove < 140;
      lastMove = now;
      move(dir, instant);
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
    setTimeout(() => {
      if (document.activeElement === document.body || !document.activeElement) {
        if (document.hasFocus()) focusEl(initialTarget(), true);
      }
    }, 50);
  });
}

export function rememberInGroup(group: string, fk: string) {
  lastInGroup.set(group, fk);
}
