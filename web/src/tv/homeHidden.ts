// Remove from Up Next, or Mark as watched on a Home card, takes the card off
// Home at once, before the server answers, and the cards to its right slide
// over. Home keeps leaving it out for a while, so a refetch that raced the
// change can't bring it back.
const hidden = new Map<number, number>();
const KEEP_MS = 10 * 60_000;

export function hideOnHome(id: number) {
  hidden.set(id, Date.now());
  window.dispatchEvent(new CustomEvent("tv:home-hidden", { detail: id }));
}

/** Marked unwatched again: it may come back. */
export function unhideOnHome(id: number) {
  if (hidden.delete(id)) window.dispatchEvent(new CustomEvent("tv:home-hidden", { detail: id }));
}

export function hiddenOnHome(id: number): boolean {
  const at = hidden.get(id);
  if (at === undefined) return false;
  if (Date.now() - at > KEEP_MS) {
    hidden.delete(id);
    return false;
  }
  return true;
}
