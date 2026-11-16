// Full episode guide from TMDB (GET /api/items/{id}/guide): every season and
// episode of a show, aired or not, set against the library, so show pages can
// grey out what's missing and show what's coming. Shared by the TV and web UIs.
import type { Card } from "./api";

export type GuideState = "have" | "missing" | "upcoming" | "partial";

export interface GuideSeason {
  season: number;
  name: string;
  airDate?: string;
  poster?: string;
  /** The library's season; absent when none of it is here. */
  itemId?: number;
  /** All episodes TMDB lists; 0 with have > 0 means TMDB numbers it differently. */
  total: number;
  aired: number;
  have: number;
  nextAirDate?: string;
  state: GuideState;
}

export interface GuideEpisode {
  season: number;
  episode: number;
  name?: string;
  overview?: string;
  airDate?: string;
  runtime?: number;
  still?: string;
  itemId?: number;
  state: "have" | "missing" | "upcoming";
}

export interface Guide {
  available: boolean;
  message?: string;
  status?: string;
  total: number;
  aired: number;
  have: number;
  next?: GuideEpisode;
  seasons: GuideSeason[];
  season?: number;
  episodes: GuideEpisode[];
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** "3 Dec", or "3 Dec 2027" when it isn't this year. */
export function fmtAirDate(d?: string) {
  if (!d) return "";
  const [y, m, day] = d.split("-").map(Number);
  if (!y || !m || !day) return d;
  const s = `${day} ${MONTHS[m - 1]}`;
  return y === new Date().getFullYear() ? s : `${s} ${y}`;
}

/** What a missing episode says instead of its runtime. */
export function episodeStatus(e: GuideEpisode) {
  if (e.state === "upcoming") return e.airDate ? `Coming ${fmtAirDate(e.airDate)}` : "Coming soon";
  if (e.state === "missing") return "Not in library";
  return "";
}

/** A season card's subtitle. */
export function seasonSub(s: GuideSeason | undefined, local?: Card) {
  const watched = local?.viewedLeafCount ? ` · ${local.viewedLeafCount} watched` : "";
  const have = local?.leafCount ?? s?.have ?? 0;
  if (!s) return `${have} episode${have === 1 ? "" : "s"}${watched}`;
  const eps = (n: number) => `${n} episode${n === 1 ? "" : "s"}`;
  if (!s.itemId) {
    if (s.state === "upcoming") return s.nextAirDate || s.airDate ? `Coming ${fmtAirDate(s.nextAirDate || s.airDate)}` : "Coming soon";
    return `Not in library · ${eps(s.total)}`;
  }
  if (!s.total || s.have >= s.total) return `${eps(have)}${watched}`;
  return `${have} of ${eps(s.total)}${watched}`;
}

/** A virtual card for something not in the library, so cards can draw it. */
export function seasonCard(s: GuideSeason, show: Card): Card {
  return {
    id: -(s.season + 1),
    libraryId: show.libraryId,
    kind: "season",
    title: s.name || `Season ${s.season}`,
    poster: s.poster,
    showPoster: show.poster,
    seasonNumber: s.season,
    parentId: show.id,
    addedAt: 0,
  };
}

export function episodeCard(e: GuideEpisode, parent: Card | undefined, showBackdrop?: string): Card {
  return {
    id: -(e.season * 100000 + e.episode + 1),
    libraryId: parent?.libraryId ?? 0,
    kind: "episode",
    title: e.name || `Episode ${e.episode}`,
    backdrop: e.still,
    showBackdrop,
    seasonNumber: e.season,
    episodeNumber: e.episode,
    runtime: e.runtime,
    overview: e.overview,
    addedAt: 0,
  };
}

export type SeasonEntry = { season: number; card?: Card; guide?: GuideSeason };

/** The library's seasons with the ones you don't have slotted in. */
export function mergeSeasons(local: Card[], guide?: Guide): SeasonEntry[] {
  const out: SeasonEntry[] = local.map((c) => ({
    season: c.seasonNumber ?? 0,
    card: c,
    guide: guide?.seasons.find((s) => s.itemId === c.id),
  }));
  if (guide?.available) {
    for (const s of guide.seasons) if (!s.itemId) out.push({ season: s.season, guide: s });
  }
  // Specials last, like the library.
  return out.sort((a, b) => (a.season === 0 ? 1e6 : a.season) - (b.season === 0 ? 1e6 : b.season));
}

export type EpisodeEntry = { episode: number; card?: Card; guide?: GuideEpisode };

/** A season's episodes with the missing and upcoming ones in place. */
export function mergeEpisodes(local: Card[], guide?: Guide, season?: number): EpisodeEntry[] {
  const g = guide?.available && guide.season === season ? guide.episodes : [];
  const out: EpisodeEntry[] = local.map((c) => ({
    episode: c.episodeNumber ?? 0,
    card: c,
    guide: g.find((e) => e.itemId === c.id),
  }));
  for (const e of g) if (!e.itemId) out.push({ episode: e.episode, guide: e });
  return out.sort((a, b) => a.episode - b.episode);
}

/** "8 of 10 episodes", or "" when there's nothing extra to say. */
export function countLabel(have: number, total: number) {
  if (!total || have >= total) return "";
  return `${have} of ${total} episodes`;
}
