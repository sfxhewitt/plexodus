// Remove from Up Next takes the card off Home at once, before the server
// answers, and the cards to its right slide over. Home keeps leaving it out
// for a while, so a refetch that raced the removal can't bring it back.
const hidden = new Map<number, number>();
const KEEP_MS = 10 * 60_000;

export function hideFromUpNext(id: number) {
  hidden.set(id, Date.now());
  window.dispatchEvent(new CustomEvent("tv:upnext-hidden", { detail: id }));
}

export function hiddenFromUpNext(id: number): boolean {
  const at = hidden.get(id);
  if (at === undefined) return false;
  if (Date.now() - at > KEEP_MS) {
    hidden.delete(id);
    return false;
  }
  return true;
}
