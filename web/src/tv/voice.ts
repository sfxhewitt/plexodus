// "Play Mobland", "continue Mobland", "Mobland season 2 episode 3": a title
// asked for by voice (the app's plexodus://play link, from LUMEN's Alexa
// skill) is found in the library and played like pressing Play on it.
import { api, Card, Detail } from "../api";
import { playCard, playItem } from "./play";
import { norm, score, VoiceAsk } from "./voiceAsk";

export { parseVoice } from "./voiceAsk";

type Results = { movies: Card[]; shows: Card[]; episodes: Card[] };

/** The library item that best fits the title, trying a few spellings ("mob land" finds MobLand). */
export async function findTitle(title: string): Promise<Card | null> {
  const words = norm(title).split(" ").filter(Boolean);
  const tries = [norm(title), words.join(""), words.slice(0, 2).join(" "), words[0]].filter((q, i, all) => q && all.indexOf(q) === i);
  let best: { c: Card; s: number } | null = null;
  for (const q of tries) {
    const r = await api.get<Results>(`/api/search?q=${encodeURIComponent(q)}`);
    for (const c of [...r.shows, ...r.movies, ...r.episodes]) {
      const s = score(c, title);
      if (!best || s > best.s) best = { c, s };
    }
    if (best && best.s >= 65) break;
  }
  return best && best.s >= 40 ? best.c : null;
}

/**
 * Finds and plays what was asked for. Returns the item whose page should sit
 * under the player (the show or film), or throws with what to tell the person.
 */
export async function playVoice(ask: VoiceAsk): Promise<number> {
  if (!ask.title) throw new Error("Say what you'd like to watch.");
  const hit = await findTitle(ask.title);
  if (!hit) throw new Error(`Couldn't find “${ask.title}”`);
  if (hit.kind === "movie") {
    await playItem(hit.id, { fromStart: ask.fromStart });
    return hit.id;
  }
  const showId = hit.kind === "show" ? hit.id : hit.showId || hit.id;
  const show = await api.get<Detail>(`/api/items/${showId}`);
  if (ask.season != null || ask.episode != null || ask.fromStart) {
    const seasons = show.children.filter((c) => c.kind === "season");
    // "Episode 3" alone means in the season being watched.
    const wanted = ask.season ?? (ask.episode != null ? show.onDeck?.seasonNumber : undefined);
    const season = wanted != null ? seasons.find((s) => s.seasonNumber === wanted) : seasons.find((s) => (s.seasonNumber ?? 0) > 0) || seasons[0];
    if (!season) throw new Error(`${show.title} has no season ${wanted}`);
    const eps = (await api.get<Detail>(`/api/items/${season.id}`)).children;
    const ep = ask.episode != null ? eps.find((e) => e.episodeNumber === ask.episode) : eps[0];
    if (!ep) throw new Error(`${show.title} season ${season.seasonNumber} has no episode ${ask.episode}`);
    await playItem(ep.id, { fromStart: ask.fromStart });
    return show.id;
  }
  await playCard(show);
  return show.id;
}
