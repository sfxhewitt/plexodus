import { useEffect, useState } from "react";
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

/**
 * Removed from suggestions: the server leaves it out from now on, so this
 * only has to cover the Home already on screen (a day is plenty).
 */
export const hideSuggestionOnHome = (id: number) => hideOnHome(id, 24 * 3600_000);

/** Everything removed from suggestions was put back: show it all again. */
export function unhideAllOnHome() {
  hidden.clear();
  replaced.clear();
  save();
  window.dispatchEvent(new CustomEvent("tv:home-hidden", { detail: 0 }));
}

/** Marked unwatched again: it may come back. */
export function unhideOnHome(id: number) {
  forgetFinished(id);
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

/** Same rule as the server: past 90% (as Plex), or into the closing credits. */
export function finishedAt(position: number, duration: number, creditsStart?: number) {
  if (!(duration > 0)) return false;
  if (position / duration >= 0.9) return true;
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
  finishedHere.set(id, Date.now());
  window.dispatchEvent(new CustomEvent("plexodus:finished", { detail: id }));
}

// Titles the player just finished. Their pages show them as watched at once
// (Watched, Mark unwatched, no resume point) instead of waiting for the
// server: its answer can still be the one from before the last progress
// report landed, and a page that kept its old copy would say "Mark watched".
const FINISHED_MS = 2 * 60_000;
const finishedHere = new Map<number, number>();

export function justFinished(id: number): boolean {
  const at = finishedHere.get(id);
  if (at === undefined) return false;
  if (Date.now() - at > FINISHED_MS) {
    finishedHere.delete(id);
    return false;
  }
  return true;
}

/** Marked unwatched by hand: the server's answer counts again. */
export function forgetFinished(id: number) {
  finishedHere.delete(id);
}

/**
 * A title page as it should look right now: the title, or any of its
 * episodes, that the player just finished shows as watched. Once the server
 * agrees nothing changes.
 */
export function withFinished<T extends { id: number; kind: string; watched?: boolean; position?: number; children?: Card[]; onDeck?: Card }>(d: T): T {
  if (!finishedHere.size) return d;
  const fix = <C extends { id: number; watched?: boolean; position?: number }>(c: C): C =>
    justFinished(c.id) && !c.watched ? { ...c, watched: true, position: 0 } : c;
  let out = fix(d);
  if (d.children?.some((c) => justFinished(c.id) && !c.watched)) out = { ...out, children: d.children.map(fix) };
  // A show's "next up" that was just finished: the server will name the next
  // episode shortly; until then don't offer to resume the finished one.
  if (d.onDeck && justFinished(d.onDeck.id) && !d.onDeck.watched) out = { ...out, onDeck: fix(d.onDeck) };
  return out;
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
  const on = (e: Event) => {
    // An up-to-date answer starts the count again for the next change.
    if (!(e as CustomEvent<boolean>).detail) {
      tries = 0;
      return;
    }
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

/** Re-renders when the player finishes a title (see withFinished). */
export function useFinishedTick() {
  const [n, setN] = useState(0);
  useEffect(() => {
    const on = () => setN((x) => x + 1);
    window.addEventListener("plexodus:finished", on);
    return () => window.removeEventListener("plexodus:finished", on);
  }, []);
  return n;
}
