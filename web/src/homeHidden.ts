import type { Card } from "./api";

// Remove from Up Next, Mark as watched, or finishing something in the player
// takes the card off Home at once, before the server answers, and the cards
// to its right slide over (or a finished episode's card becomes the next
// episode). Home keeps leaving it out for a while, so a refetch that raced
// the change can't bring it back.
// Kept in localStorage too: Android may close the app in the background, and
// a Home fetched on its return can still be the server's previous one.
const STORE = "plexodus.homeHidden";
const KEEP_MS = 10 * 60_000;
const hidden = new Map<number, { at: number; keep: number }>(load());
const replaced = new Map<number, Card>();

function load(): [number, { at: number; keep: number }][] {
  try {
    const raw = JSON.parse(localStorage.getItem(STORE) || "[]") as [number, number, number][];
    return raw.filter((x) => Date.now() - x[1] < x[2]).map(([id, at, keep]) => [id, { at, keep }]);
  } catch {
    return [];
  }
}

function save() {
  try {
    localStorage.setItem(STORE, JSON.stringify([...hidden].map(([id, h]) => [id, h.at, h.keep])));
  } catch {
    /* private mode or full: memory only */
  }
}

const changed = (id: number) => {
  save();
  window.dispatchEvent(new CustomEvent("tv:home-hidden", { detail: id }));
};

export function hideOnHome(id: number, keepMs = KEEP_MS) {
  hidden.set(id, { at: Date.now(), keep: keepMs });
  changed(id);
}

/** Marked unwatched again: it may come back. */
export function unhideOnHome(id: number) {
  const had = hidden.delete(id);
  if (replaced.delete(id) || had) changed(id);
}

export function hiddenOnHome(id: number): boolean {
  const h = hidden.get(id);
  if (h === undefined) return false;
  if (Date.now() - h.at > h.keep) {
    hidden.delete(id);
    replaced.delete(id);
    save();
    return false;
  }
  return true;
}

/** Same rule as the server: past 92%, or into the closing credits. */
export function finishedAt(position: number, duration: number, creditsStart?: number) {
  if (!(duration > 0)) return false;
  if (position / duration > 0.92) return true;
  return !!creditsStart && creditsStart >= duration * 0.7 && position >= creditsStart;
}

/** Where the closing credits start, if a marker says so. */
export function creditsStart(markers: { type: string; start: number }[], duration: number) {
  const s = markers.filter((m) => m.type === "credits" && m.start >= duration * 0.7).map((m) => m.start);
  return s.length ? Math.min(...s) : undefined;
}

/**
 * A title just finished in the player: drop it from Home now. Kept out for
 * two minutes, by when the server's Home has caught up.
 */
export function finishedOnHome(id: number) {
  hideOnHome(id, 2 * 60_000);
}

/** The finished episode's card turns into the show's next one, in the same place. */
export function replaceOnHome(id: number, next: Card) {
  if (!hidden.has(id)) return;
  replaced.set(id, next);
  changed(id);
}

/**
 * Home's rows as they should look right now: hidden cards left out, a
 * finished episode swapped for the next one in the rows `swaps` allows
 * (Up Next; unless it's already there), empty rows dropped.
 */
export function homeRows<R extends { items: Card[] }>(rows: R[], swaps: (r: R) => boolean = () => true): R[] {
  return rows
    .map((r) => {
      const swap = swaps(r);
      const ids = new Set(r.items.map((c) => c.id));
      const items: Card[] = [];
      for (const c of r.items) {
        if (!hiddenOnHome(c.id)) {
          items.push(c);
          continue;
        }
        const next = swap ? replaced.get(c.id) : undefined;
        if (next && !ids.has(next.id) && !hiddenOnHome(next.id)) {
          ids.add(next.id);
          items.push(next);
        }
      }
      return { ...r, items };
    })
    .filter((r) => r.items.length);
}

// Leaving the player, Home is often fetched before the player's last
// progress report has been saved, and so comes back with the old Up Next.
// Pages ask again a moment later.
let playerClosedAt = 0;
export const playerClosed = () => (playerClosedAt = Date.now());
export const playerJustClosed = () => Date.now() - playerClosedAt < 10_000;

/**
 * Home pages: when the server says the Home it gave is its previous one,
 * ask again a few seconds later (a few times at most).
 */
export function onHomeStale(reload: () => void) {
  let tries = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const on = () => {
    if (tries >= 6 || timer) return;
    tries++;
    timer = setTimeout(() => {
      timer = undefined;
      reload();
    }, 3000);
  };
  window.addEventListener("plexodus:home-stale", on);
  return () => {
    clearTimeout(timer);
    window.removeEventListener("plexodus:home-stale", on);
  };
}
