import { api, Card, Detail, episodeLabel, Marker, PlayInfo } from "../api";
import { native, nativePlay } from "../native";

type Listener = (busy: string | null) => void;
const listeners = new Set<Listener>();
export const onPlayBusy = (l: Listener) => {
  listeners.add(l);
  return () => void listeners.delete(l);
};
const setBusy = (b: string | null) => listeners.forEach((l) => l(b));

let lastPlayed: Detail | null = null;
export const lastPlayedItem = () => lastPlayed;

/**
 * Hands an item to the app's built-in player (libVLC, plays every format
 * directly). Asks the server for the stream without waiting for a codec
 * probe: the player works the format out itself, so playback starts sooner.
 */
export async function playItem(id: number, opts: { fromStart?: boolean } = {}) {
  setBusy("Starting…");
  try {
    const d = await api.get<Detail>(`/api/items/${id}`);
    if (!d.files.length) throw new Error("There's no media file for this title.");
    const [p, m] = await Promise.all([
      api.get<PlayInfo>(`/api/play/${d.files[0].id}?probe=0`),
      api.get<Marker[]>(`/api/items/${d.id}/markers`).catch(() => [] as Marker[]),
    ]);
    if (!native) {
      // In a desktop browser with ?tv=1 there's no native player; use the web one.
      window.location.href = `/play/${d.id}${opts.fromStart ? "?from=0" : ""}`;
      return;
    }
    lastPlayed = d;
    const title = d.kind === "episode" ? d.showTitle || d.show?.title || d.title : d.title;
    const subtitle = d.kind === "episode" ? `${episodeLabel(d)} · ${d.title}` : d.year ? String(d.year) : "";
    nativePlay({
      itemId: d.id,
      kind: d.kind,
      url: p.externalUrl,
      title,
      subtitle,
      startSeconds: opts.fromStart ? 0 : p.position || 0,
      durationSeconds: p.file.duration || d.files[0].duration || 0,
      markers: m.map((x) => ({ type: x.type, start: x.start, end: x.end })),
    });
    // The player activity takes a moment to cover the page.
    setTimeout(() => setBusy(null), 1500);
  } catch (e) {
    setBusy(null);
    throw e;
  }
}

/** What to play for a card: the item itself, or a show's next episode. */
export async function playCard(c: Card) {
  if (c.kind === "movie" || c.kind === "episode") return playItem(c.id);
  const d = await api.get<Detail>(`/api/items/${c.id}`);
  const next = d.onDeck || d.children[0];
  if (!next) throw new Error("Nothing to play yet.");
  if (next.kind === "season") {
    const s = await api.get<Detail>(`/api/items/${next.id}`);
    if (!s.children[0]) throw new Error("Nothing to play yet.");
    return playItem(s.children[0].id);
  }
  return playItem(next.id);
}
