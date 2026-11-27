// A small fake Plexodus library served through Playwright's request routing,
// so the TV UI renders the same screens every run without a real server.
// Shapes follow web/src/api.ts.
import type { Page, Route } from "@playwright/test";
import type { Card, Detail, HomeData, Library, Row, Wrapped } from "../src/api";

const DAY = 86400;
const NOW = 1_790_000_000;

const MOVIE_TITLES = [
  "Night Harbour", "The Glass Orchard", "Low Tide", "Paper Lanterns", "Iron Meridian", "The Quiet Year",
  "Northbound", "Ashfall", "A Room Upstairs", "Salt and Static", "The Cartographer", "Blue Hour",
  "Second Sun", "Hollow Creek", "The Long Shift", "Undertow", "Midnight Ferry", "Copper Sky",
  "The Last Signal", "Winter Lines", "Small Hours", "Fault Line", "The Orchid Thief's Son", "Red Delta",
];
const SHOW_TITLES = ["Station Eleven Below", "The Vale", "Dead Reckoning", "Harbourmaster", "Kestrel", "Overground", "The Bureau of Lost Things", "Signal Fire"];
const GENRES = ["Drama", "Thriller", "Science Fiction", "Comedy", "Mystery", "Crime"];

// Made-up synopses for the made-up titles above.
const LOGLINES = [
  "A harbour pilot takes one last night crossing and finds the lights on the far shore were never meant for ships.",
  "Three sisters return to the family orchard for one summer and learn what their mother buried under the oldest tree.",
  "When the tide goes out further than anyone remembers, a fishing town walks out to see what it left behind.",
  "A lantern maker and a night-market thief strike a deal that neither of them intends to keep.",
  "An engineer racing to finish a railway across the mountains discovers the survey maps were drawn to hide something.",
  "Twelve months, one small house, and a family learning to talk to each other again.",
];
const SHOW_LOGLINES = [
  "Deep under a flooded city, the crew of a research station keep the lights on and the secrets in.",
  "A quiet valley town, a new doctor, and a winter that refuses to end.",
  "A navigator with no memory of the voyage is the only survivor who knows where the ship was going.",
];

export const MOVIES_LIB = 1;
export const SHOWS_LIB = 2;

const movies: Card[] = [];
for (let i = 0; i < 60; i++) {
  const base = MOVIE_TITLES[i % MOVIE_TITLES.length];
  const title = i < MOVIE_TITLES.length ? base : `${base} ${Math.floor(i / MOVIE_TITLES.length) + 1}`;
  movies.push({
    id: 100 + i, libraryId: MOVIES_LIB, kind: "movie", title, year: 1985 + (i % 38),
    poster: `/p/m${i}.jpg`, backdrop: `/b/m${i}.jpg`, rating: 5 + ((i * 7) % 45) / 10, runtime: 85 + (i * 13) % 70,
    addedAt: NOW - i * DAY, watched: i % 5 === 0, position: i === 3 ? 2400 : undefined, duration: i === 3 ? 6000 : undefined,
    overview: LOGLINES[i % LOGLINES.length],
    // Rotten Tomatoes: most films have both scores, some only one, a few none.
    rtCritic: i % 9 === 8 ? undefined : (i * 37 + 41) % 101,
    rtAudience: i % 9 === 8 || i % 11 === 5 ? undefined : (i * 53 + 62) % 61 + 39,
  });
}

const shows: Card[] = [];
const seasons = new Map<number, Card[]>(); // showId -> seasons
const episodes = new Map<number, Card[]>(); // seasonId -> episodes
let nextId = 1000;
SHOW_TITLES.forEach((title, si) => {
  const showId = 200 + si;
  const show: Card = {
    id: showId, libraryId: SHOWS_LIB, kind: "show", title, year: 2008 + si, poster: `/p/s${si}.jpg`, backdrop: `/b/s${si}.jpg`,
    rating: 7 + (si % 3) / 2, addedAt: NOW - si * 2 * DAY, leafCount: 0, viewedLeafCount: 0,
    overview: SHOW_LOGLINES[si % SHOW_LOGLINES.length],
    rtCritic: si % 4 === 3 ? undefined : 70 + (si * 13) % 30,
    rtAudience: si % 4 === 3 ? undefined : 60 + (si * 17) % 39,
  };
  const ss: Card[] = [];
  const nSeasons = si === 0 ? 4 : 1 + (si % 3);
  for (let s = 1; s <= nSeasons; s++) {
    const seasonId = nextId++;
    const eps: Card[] = [];
    const nEps = si === 0 && s === 1 ? 10 : 6;
    for (let e = 1; e <= nEps; e++) {
      eps.push({
        id: nextId++, libraryId: SHOWS_LIB, kind: "episode", title: `Chapter ${e}`, seasonNumber: s, episodeNumber: e,
        parentId: seasonId, showId, showTitle: title, showPoster: show.poster, showBackdrop: show.backdrop,
        backdrop: `/b/e${si}-${s}-${e}.jpg`, runtime: 42 + e, addedAt: NOW - (si * 2 + s) * DAY,
        watched: si === 0 && s === 1 && e < 3, position: si === 0 && s === 1 && e === 3 ? 900 : undefined,
        duration: si === 0 && s === 1 && e === 3 ? 2700 : undefined, overview: `Episode ${e} of season ${s}.`,
      });
    }
    episodes.set(seasonId, eps);
    ss.push({
      id: seasonId, libraryId: SHOWS_LIB, kind: "season", title: `Season ${s}`, seasonNumber: s, parentId: showId, showId,
      showTitle: title, poster: `/p/s${si}-${s}.jpg`, addedAt: show.addedAt, leafCount: eps.length,
      viewedLeafCount: eps.filter((x) => x.watched).length,
    });
  }
  show.leafCount = ss.reduce((n, s) => n + (s.leafCount || 0), 0);
  show.viewedLeafCount = ss.reduce((n, s) => n + (s.viewedLeafCount || 0), 0);
  seasons.set(showId, ss);
  shows.push(show);
});

const allEpisodes = [...episodes.values()].flat();
const byId = new Map<number, Card>([...movies, ...shows, ...[...seasons.values()].flat(), ...allEpisodes].map((c) => [c.id, c]));

/**
 * Episode guide (TMDB's full list) for the first show: season 1 has two
 * more episodes than the library (one aired, one coming), and season 5 is
 * announced but not out. Other shows have no guide.
 */
function guide(id: number, seasonParam: string | null) {
  const c = byId.get(id);
  const showId = c?.kind === "season" ? c.parentId! : id;
  const want = c?.kind === "season" ? c.seasonNumber! : seasonParam != null ? Number(seasonParam) : undefined;
  if (showId !== shows[0].id) return { available: false, total: 0, aired: 0, have: 0, seasons: [], episodes: [], season: want };
  const local = seasons.get(showId)!;
  const gs = [1, 2, 3, 4, 5].map((n) => {
    const ls = local.find((s) => s.seasonNumber === n);
    const total = n === 1 ? 12 : n === 5 ? 8 : 6;
    const eps = Array.from({ length: total }, (_, i) => {
      const e = i + 1;
      const have = ls ? episodes.get(ls.id)!.find((x) => x.episodeNumber === e) : undefined;
      const upcoming = n === 5 || (n === 1 && e === 12);
      const airDate = n === 5 ? (e === 1 ? "2099-03-01" : "") : n === 1 && e === 12 ? "2099-01-15" : `20${10 + n}-0${1 + (e % 9)}-01`;
      return { season: n, episode: e, name: have?.title || `Unseen ${n}x${e}`, overview: `Guide entry for ${n}x${e}.`, airDate,
        still: `/b/g${n}-${e}.jpg`, runtime: 44, itemId: have?.id, state: have ? "have" : upcoming ? "upcoming" : "missing" };
    });
    const aired = eps.filter((e) => e.state !== "upcoming").length;
    const haveN = eps.filter((e) => e.itemId).length;
    return {
      meta: { season: n, name: `Season ${n}`, airDate: n === 5 ? "2099-03-01" : `20${10 + n}-01-01`, poster: ls?.poster || `/p/g${n}.jpg`,
        itemId: ls?.id, total, aired, have: haveN, nextAirDate: eps.find((e) => e.state === "upcoming" && e.airDate)?.airDate,
        state: haveN && haveN >= aired ? "have" : haveN ? "partial" : aired ? "missing" : "upcoming" },
      eps,
    };
  });
  const all = gs.flatMap((s) => s.eps);
  return {
    available: true, status: "Returning Series",
    total: all.length, aired: all.filter((e) => e.state !== "upcoming").length, have: all.filter((e) => e.itemId).length,
    next: all.find((e) => e.airDate === "2099-01-15"),
    seasons: gs.map((s) => s.meta), season: want, episodes: want != null ? gs.find((s) => s.meta.season === want)?.eps ?? [] : [],
  };
}

/** Well-known ids the specs navigate to. */
/** Artwork path -> the title it belongs to (store screenshots letter it onto the poster). */
export const artTitles = new Map<string, string>();
for (const c of [...movies, ...shows]) {
  if (c.poster) artTitles.set(c.poster, c.title);
  if (c.backdrop) artTitles.set(c.backdrop, c.title);
}

export const ids = {
  movie: movies[0].id,
  collected: movies[10].id, // in "The Atlas Collection"
  extras: movies[20].id, // has cast and "more like this"
  show: shows[0].id,
  season: seasons.get(shows[0].id)![0].id,
  episode: episodes.get(seasons.get(shows[0].id)![0].id)![2].id,
};

const libraries: Library[] = [
  { id: MOVIES_LIB, name: "Movies", kind: "movies", paths: ["/media/movies"], trustDirMtime: false, count: movies.length, fileCount: movies.length, unprobed: 0, pendingMetadata: 0, totalSize: 4e12, backdrop: movies[0].backdrop },
  { id: SHOWS_LIB, name: "TV Shows", kind: "shows", paths: ["/media/tv"], trustDirMtime: false, count: shows.length, fileCount: allEpisodes.length, unprobed: 0, pendingMetadata: 0, totalSize: 9e12, backdrop: shows[0].backdrop },
];

function file(c: Card) {
  return {
    id: c.id * 10, path: `/media/${c.title}.mkv`, name: `${c.title}.mkv`, size: 8e9, container: "matroska", videoCodec: "hevc",
    audioCodec: "eac3", width: 3840, height: 2160, duration: (c.runtime || 60) * 60, probed: true,
    streams: [
      { index: 0, type: "video" as const, codec: "hevc" },
      { index: 1, type: "audio" as const, codec: "eac3", channels: 6, language: "eng", default: true },
      { index: 2, type: "subtitle" as const, codec: "subrip", language: "eng" },
    ],
  };
}

// One franchise: three films that other tests leave alone.
const COLLECTION = {
  items: () => movies.slice(10, 13),
  card: () => ({ id: 900, name: "The Atlas Collection", poster: movies[10].poster, backdrop: movies[10].backdrop, count: 3, watched: 0,
    source: "tmdb" as const, kind: "movie" as const, group: "Franchises & series" }),
};

/** Collections from Plex: enough studios for a "See all", and one of shows. */
const PLEX_COLLECTIONS = () => [
  ...Array.from({ length: 34 }, (_, i) => ({ id: 1_000_000_000_100 + i, name: `Studio ${i + 1}`, poster: movies[i].poster,
    count: 2, watched: 0, source: "plex" as const, kind: "movie" as const, group: "Studios & networks" })),
  { id: 1_000_000_000_200, name: "Miniseries", poster: shows[0].poster, count: 1, watched: 0, source: "plex" as const,
    kind: "show" as const, group: "TV collections", summary: "Short and sweet." },
];

function collectionsList() {
  const all = [COLLECTION.card(), ...PLEX_COLLECTIONS()];
  const groups: { name: string; collections: typeof all }[] = [];
  for (const c of all) {
    let g = groups.find((x) => x.name === c.group);
    if (!g) groups.push((g = { name: c.group, collections: [] }));
    g.collections.push(c);
  }
  return { groups, count: all.length, pending: 0 };
}

function detail(c: Card): Detail {
  const base: Detail = { ...c, genres: GENRES.slice(c.id % 3, (c.id % 3) + 3), metaState: "ok", files: [], children: [], tagline: "Every map ends somewhere." };
  if (c.kind === "movie") {
    const inCol = COLLECTION.items().some((f) => f.id === c.id);
    return { ...base, files: [file(c)], ...(inCol ? { collection: COLLECTION.card(), collectionItems: COLLECTION.items() } : {}) };
  }
  if (c.kind === "show") {
    const ss = seasons.get(c.id)!;
    const eps = ss.flatMap((s) => episodes.get(s.id)!);
    return { ...base, children: ss, onDeck: eps.find((e) => e.position) || eps.find((e) => !e.watched) };
  }
  if (c.kind === "season") {
    const show = byId.get(c.showId!)!;
    return { ...base, children: episodes.get(c.id)!, show, parent: show, siblings: seasons.get(c.showId!) };
  }
  const season = byId.get(c.parentId!)!;
  return { ...base, files: [file(c)], parent: season, show: byId.get(c.showId!), siblings: episodes.get(season.id) };
}

interface UserState {
  hiddenContinue: Set<number>;
  /** Removed from suggestions, in the order removed. */
  noSuggest: number[];
  watchlist: number[];
  /** Picks: never-show / must-have genres, and what was suggested, skipped or hidden. */
  genres: { exclude: string[]; require: string[] };
  shown: Set<number>;
  hidden: Set<number>;
  skips: Map<number, number>;
  votes: Map<number, number>;
  seen: Set<number>;
  /** Removed from History. */
  historyHidden?: Set<number>;
}

/**
 * History: what was played, newest first, on three different days whatever
 * the time of day (two plays today, one yesterday, one three days ago).
 * Times counted back from "now" alone fell into the wrong days in the
 * minutes after midnight.
 */
function history(st: UserState) {
  const now = Math.floor(Date.now() / 1000);
  const midnight = new Date();
  midnight.setHours(0, 0, 0, 0);
  const today = Math.floor(midnight.getTime() / 1000);
  const ep = allEpisodes[0];
  const list = [
    { ...movies[3], playedAt: Math.max(today, now - 600) },
    { ...ep, position: 40, duration: 2700, watched: false, playedAt: Math.max(today, now - 1200) },
    { ...movies[0], position: undefined, duration: 6000, watched: true, playedAt: today - 3600 },
    { ...movies[7], position: 300, duration: 6500, watched: false, playedAt: today - 2 * 86400 - 3600 },
  ];
  return { items: list.filter((c) => !st.historyHidden?.has(c.id)), more: false, before: 0 };
}

/**
 * Smart rows and the Wrapped tile, added to Home when the page was opened
 * with ?extras=1 (so the other specs keep the plain Home they count rows on).
 */
function extras(h: HomeData, gone: number[] = []): HomeData {
  const ok = (c: Card) => !gone.includes(c.id);
  const season = seasons.get(shows[0].id)![0];
  const smart: Row[] = [
    {
      title: "Finish a Season This Weekend", kind: "poster", smart: "season",
      items: [{ ...season, title: shows[0].title, showPoster: shows[0].poster, showBackdrop: shows[0].backdrop, note: "Season 1 · 8 left · 6h 4m" }],
    },
    { title: `Because You Watched ${movies[0].title}`, kind: "poster", smart: "because", items: movies.slice(30, 44).filter(ok) },
  ];
  const later: Row[] = [
    { title: "Under 100 Minutes", kind: "poster", smart: "short", items: movies.filter((m) => (m.runtime || 0) <= 100).filter(ok) },
    { title: "Forgotten Gems", kind: "poster", smart: "gems", items: [...movies.slice(44, 52), shows[5], shows[6]].filter(ok) },
  ];
  return { ...h, rows: [h.rows[0], ...smart, ...h.rows.slice(1), ...later], wrapped: { year: 2026, plays: 812 } };
}

/** A year of Wrapped for the mock user. */
export function wrapped(year = 2026): Wrapped {
  const hours = Array.from({ length: 24 }, (_, h) => (h >= 18 ? (24 - Math.abs(22 - h) * 5) * 3600 : h < 2 ? 9 * 3600 : h * 600));
  return {
    year, years: [2026, 2025, 2024], plays: 812, movies: 96, episodes: 716, shows: 41, seconds: 1_480_000, days: 233,
    topShows: shows.slice(0, 5).map((c, i) => ({ card: c, plays: 80 - i * 11, seconds: (80 - i * 11) * 2700 })),
    topMovies: movies.slice(0, 5).map((c, i) => ({ card: c, plays: i === 0 ? 3 : 1, seconds: (i === 0 ? 3 : 1) * 6600 })),
    topGenres: GENRES.slice(0, 5).map((name, i) => ({ name, seconds: (300 - i * 50) * 3600 })),
    hours, weekdays: [5, 3, 3, 4, 4, 7, 8].map((x) => x * 36000), months: [8, 6, 9, 7, 5, 4, 6, 9, 12, 10, 11, 4].map((x) => x * 36000),
    peakHour: 22, persona: "The Night Owl", blurb: "Your peak was after dark. The best stories start late.",
    streak: { days: 23, start: `${year}-03-02`, end: `${year}-03-24` },
    binge: { show: shows[0], episodes: 11, date: `${year}-02-14`, seconds: 11 * 2700 },
    biggestDay: { date: `${year}-02-14`, plays: 12, seconds: 13 * 2700 },
    first: { card: shows[1], at: NOW },
  };
}

function home(st: Pick<UserState, "hiddenContinue" | "watchlist" | "noSuggest"> = { hiddenContinue: new Set(), watchlist: [], noSuggest: [] }): HomeData {
  // As on the server: a removed title is in no suggestion (Up Next and the Watchlist keep it).
  const ok = (c: Card) => !st.noSuggest.includes(c.id);
  const continuing = [...movies, ...allEpisodes].filter((c) => c.position && !st.hiddenContinue.has(c.id));
  const wl = st.watchlist.map((id) => ({ ...byId.get(id)!, watchlisted: true }));
  const onDeck = shows.slice(1, 6).map((s) => episodes.get(seasons.get(s.id)![0].id)![0]);
  return {
    hero: movies.slice(0, 5).filter(ok),
    rows: [
      { title: "Up Next", kind: "landscape", items: continuing },
      ...(wl.length ? [{ title: "Watchlist", kind: "poster" as const, items: wl }] : []),
      { title: "On Deck", kind: "landscape", items: onDeck },
      { title: "Recently Added Movies", kind: "poster", link: `/library/${MOVIES_LIB}?sort=added`, items: movies.slice(0, 20).filter(ok) },
      { title: "Recently Added TV", kind: "poster", link: `/library/${SHOWS_LIB}?sort=added`, items: shows.filter(ok) },
      { title: "Top Rated", kind: "poster", items: [...movies].sort((a, b) => (b.rating || 0) - (a.rating || 0)).slice(0, 15).filter(ok) },
    ],
    ...(st.noSuggest.length ? { removed: st.noSuggest.length } : {}),
  };
}

// The mock has no mood scores: a mood is a couple of genres, a content limit rules one out.
const MOOD_GENRES: [string, string[]][] = [["fg", ["Comedy"]], ["th", ["Thriller", "Crime"]], ["cr", ["Drama"]], ["la", ["Comedy"]], ["mb", ["Mystery", "Science Fiction"]], ["ch", ["Comedy", "Drama"]], ["ad", ["Thriller"]]];
const MOOD_NAMES: Record<string, string> = { fg: "Feel-good", th: "Thriller", cr: "Have a cry", la: "Laugh", mb: "Mind-bender", ch: "Chill", ad: "Adrenaline" };
const CONTENT_OUT: Record<string, string> = { noviolence: "Crime", noscares: "Thriller", family: "Thriller", nosex: "Drama" };

function libraryItems(id: number, q: URLSearchParams) {
  let items = id === MOVIES_LIB ? [...movies] : id === SHOWS_LIB ? [...shows] : [];
  const genre = q.get("genre");
  if (genre) items = items.filter((c) => detail(c).genres.includes(genre));
  const moodGenres = MOOD_GENRES.find(([k]) => k === q.get("mood"))?.[1];
  if (moodGenres) items = items.filter((c) => detail(c).genres.some((g) => moodGenres.includes(g)));
  for (const k of (q.get("content") || "").split(",").filter(Boolean)) {
    items = items.filter((c) => (k === "romance" ? detail(c).genres.includes("Drama") : !detail(c).genres.includes(CONTENT_OUT[k])));
  }
  const no = (q.get("nogenre") || "").split(",").filter(Boolean);
  const need = (q.get("needgenre") || "").split(",").filter(Boolean);
  if (no.length) items = items.filter((c) => !detail(c).genres.some((g) => no.includes(g)));
  if (need.length) items = items.filter((c) => detail(c).genres.some((g) => need.includes(g)));
  if (q.get("filter") === "unwatched") items = items.filter((c) => !c.watched);
  if (q.get("filter") === "inprogress") items = items.filter((c) => c.position);
  if (q.get("filter") === "watched") items = items.filter((c) => c.watched);
  const decade = q.get("decade");
  if (decade) items = items.filter((c) => Math.floor((c.year || 0) / 10) * 10 === Number(decade));
  const rating = q.get("rating");
  if (rating) items = items.filter((c) => (c.rating || 0) >= Number(rating));
  const rt = q.get("rt");
  if (rt) items = items.filter((c) => (rt === "audience" ? (c.rtAudience ?? -1) >= 80 : rt === "rotten" ? c.rtCritic != null && c.rtCritic < 60 : (c.rtCritic ?? -1) >= (rt === "90" ? 90 : 60)));
  const sort = q.get("sort") || "title";
  const key = (c: Card) => (sort === "added" ? c.addedAt : sort === "rating" || sort === "match" ? c.rating || 0 : sort === "critics" ? c.rtCritic ?? -1 : sort === "audience" ? c.rtAudience ?? -1 : sort === "year" || sort === "release" ? c.year || 0 : c.title.toLowerCase().replace(/^(the|a|an) /, ""));
  items.sort((a, b) => (key(a) < key(b) ? -1 : key(a) > key(b) ? 1 : 0));
  if ((q.get("order") || (sort === "title" ? "asc" : "desc")) === "desc") items.reverse();
  const offset = Number(q.get("offset") || 0);
  const limit = Number(q.get("limit") || 120);
  const out: { items: Card[]; total: number; offset: number; letters?: { l: string; offset: number }[] } = { items: items.slice(offset, offset + limit), total: items.length, offset };
  if (q.get("letters") === "1") {
    out.letters = [];
    items.forEach((c, i) => {
      const ch = c.title.replace(/^(the|a|an) /i, "")[0].toUpperCase();
      const l = ch >= "A" && ch <= "Z" ? ch : "#";
      if (!out.letters!.some((x) => x.l === l)) out.letters!.push({ l, offset: i });
    });
  }
  return out;
}

// Live counts for the TV filter panel: each choice counted with the other filters applied.
function facets(id: number, q: URLSearchParams) {
  const without = (k: string) => {
    const p = new URLSearchParams(q);
    p.delete(k);
    p.set("limit", "100000");
    return libraryItems(id, p).items;
  };
  const opts = (k: string, list: [string, string, (c: Card) => boolean][]) => {
    const items = without(k);
    return list.map(([v, l, f]) => ({ v, l, count: items.filter(f).length }));
  };
  const decades = [2020, 2010, 2000, 1990, 1980].map((d) => [String(d), `${d}s`, (c: Card) => Math.floor((c.year || 0) / 10) * 10 === d] as [string, string, (c: Card) => boolean]);
  const g = without("genre");
  return {
    total: libraryItems(id, new URLSearchParams([...q, ["limit", "1"]])).total,
    facets: [
      ...(id === MOVIES_LIB ? [
        { key: "mood", label: "Mood", options: opts("mood", MOOD_GENRES.map(([k, gs]) => [k, MOOD_NAMES[k], (c: Card) => detail(c).genres.some((x) => gs.includes(x))])) },
        { key: "content", label: "Content", multi: true, options: [["noviolence", "No graphic violence"], ["noscares", "No scares"], ["family", "Family-friendly"], ["nosex", "No explicit sex"], ["romance", "Romance"]]
          .map(([v, l]) => ({ v, l, count: libraryItems(id, new URLSearchParams([...q, ["limit", "100000"], ["content", [q.get("content"), v].filter(Boolean).join(",")]])).total })) },
      ] : []),
      { key: "filter", label: "Watched", options: opts("filter", [["unwatched", "Unwatched", (c) => !c.watched], ["inprogress", "In progress", (c) => !!c.position], ["watched", "Watched", (c) => !!c.watched]]) },
      { key: "decade", label: "Decade", options: opts("decade", decades) },
      { key: "rating", label: "Rating", options: opts("rating", [["8", "8+ Outstanding", (c) => (c.rating || 0) >= 8], ["7", "7+ Great", (c) => (c.rating || 0) >= 7], ["6", "6+ Good", (c) => (c.rating || 0) >= 6]]) },
      { key: "rt", label: "Rotten Tomatoes", options: opts("rt", [["90", "Critics 90%+", (c) => (c.rtCritic ?? -1) >= 90], ["fresh", "Fresh · critics 60%+", (c) => (c.rtCritic ?? -1) >= 60], ["rotten", "Rotten · critics under 60%", (c) => c.rtCritic != null && c.rtCritic < 60], ["audience", "Audience 80%+", (c) => (c.rtAudience ?? -1) >= 80]]) },
    ],
    genres: GENRES.map((name) => ({ v: name, l: name, count: g.filter((c) => detail(c).genres.includes(name)).length })).filter((x) => x.count > 0),
  };
}

function hue(s: string) {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) % 360;
  return h;
}

/** A gradient placeholder for any /art/ request, labelled with its path. */
function artSvg(path: string) {
  const h = hue(path);
  const wide = path.includes("/b/");
  const [w, hgt] = wide ? [1280, 720] : [342, 513];
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${hgt}" viewBox="0 0 ${w} ${hgt}">
<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="hsl(${h},35%,38%)"/><stop offset="1" stop-color="hsl(${(h + 60) % 360},30%,12%)"/></linearGradient></defs>
<rect width="100%" height="100%" fill="url(#g)"/>
${wide ? "" : `<text x="50%" y="50%" fill="rgba(255,255,255,.35)" font-family="sans-serif" font-size="22" text-anchor="middle">${path.split("/").pop()}</text>`}</svg>`;
}

export interface MockOptions {
  /** Requests the mock has no answer for, collected so specs can fail on them. */
  unhandled?: string[];
  /** Give the account profiles (a kids one, and a PIN on the main one), so "Who's watching?" shows. */
  profiles?: boolean;
  /** Link "Dad"'s Plex account, so Share buttons show. */
  share?: boolean;
}

const LEVELS = [
  { maxAge: 0, label: "Little kids", detail: "G, TV-Y, TV-G, U" },
  { maxAge: 8, label: "Kids", detail: "Up to PG, TV-Y7, TV-PG" },
  { maxAge: 14, label: "Teens", detail: "Up to PG-13, TV-14, 12A" },
];

export async function mockServer(page: Page, opts: MockOptions = {}) {
  const json = (route: Route, body: unknown, status = 200) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

  // Keep runs offline and deterministic: no web fonts.
  await page.route(/fonts\.(googleapis|gstatic)\.com/, (r) => r.abort());

  await page.route("**/art/**", (route) => {
    const u = new URL(route.request().url());
    return route.fulfill({ status: 200, contentType: "image/svg+xml", body: artSvg(u.pathname + u.search) });
  });

  // A stand-in YouTube player: answers the iframe API handshake and reports
  // "playing", like the real embed does once a trailer starts.
  await page.route(/youtube(-nocookie)?\.com\//, (route) =>
    route.fulfill({
      status: 200,
      contentType: "text/html",
      body: `<!doctype html><body style="margin:0;background:#123;color:#fff;font:40px sans-serif">fake trailer<script>
addEventListener("message", function (e) {
  var m = JSON.parse(e.data);
  if (m.event === "command") {
    parent.postMessage(JSON.stringify({ event: "mockCommand", func: m.func }), "*");
    if (m.func === "pauseVideo") parent.postMessage(JSON.stringify({ event: "onStateChange", info: 2, id: 1 }), "*");
    if (m.func === "playVideo") parent.postMessage(JSON.stringify({ event: "onStateChange", info: 1, id: 1 }), "*");
    return;
  }
  if (m.event !== "listening") return;
  parent.postMessage(JSON.stringify({ event: "onReady", id: 1 }), "*");
  setTimeout(function () { parent.postMessage(JSON.stringify({ event: "onStateChange", info: 1, id: 1 }), "*"); }, 100);
});
</script></body>`,
    }),
  );

  // Per-test copy: the settings screen changes library choices.
  const libs = libraries.map((l) => ({ ...l, hidden: false, hiddenHome: false }));
  const st: UserState = { hiddenContinue: new Set(), noSuggest: [], watchlist: [], genres: { exclude: [], require: [] }, shown: new Set(), hidden: new Set(), skips: new Map(), votes: new Map(), seen: new Set() };
  // The saved genres apply to every library request, as on the server.
  const withPrefs = (q: URLSearchParams) => {
    const p = new URLSearchParams(q);
    if (st.genres.exclude.length) p.set("nogenre", st.genres.exclude.join(","));
    if (st.genres.require.length) p.set("needgenre", st.genres.require.join(","));
    return p;
  };
  const pick = (c: Card, q: URLSearchParams) => ({
    ...c, match: 90, moods: MOOD_GENRES.filter(([, gs]) => detail(c).genres.some((g) => gs.includes(g))).map(([k]) => MOOD_NAMES[k]).slice(0, 2),
    genres: detail(c).genres, blurb: detail(c).overview,
    why: `${q.get("mood") ? `Fits ${MOOD_NAMES[q.get("mood")!].toLowerCase()}: funny (90%); ` : ""}${(c.rating || 0).toFixed(1)} weighted rating from 12k votes.`,
  });
  const pickable = (q: URLSearchParams) => libraryItems(MOVIES_LIB, new URLSearchParams([...withPrefs(q), ["limit", "100000"], ["sort", "rating"]])).items
    .filter((c) => !c.watched && !st.seen.has(c.id));
  const account = { id: 1, name: "daniel", avatar: "", color: "cyan", kids: false, hasPin: !!opts.profiles, isAccount: true };
  const profiles = opts.profiles
    ? [account, { id: 2, name: "Mia", avatar: "🦊", color: "magenta", kids: true, maxAge: 8, hasPin: false, isAccount: false },
      { id: 3, name: "Sam", avatar: "🚀", color: "lime", kids: false, hasPin: false, isAccount: false }]
    : [account];
  let current = 1;
  const me = () => {
    const p = profiles.find((x) => x.id === current)!;
    return { id: p.id, username: "daniel", isAdmin: p.isAccount, allLibraries: true, libraries: [], accountId: 1, isProfile: !p.isAccount,
      name: p.name, avatar: p.avatar, color: p.color, kids: p.kids, maxAge: p.kids ? 8 : undefined };
  };

  await page.route("**/api/**", (route) => {
    const u = new URL(route.request().url());
    const p = u.pathname;
    const m = route.request().method();
    let mm: RegExpMatchArray | null;
    if (p === "/api/status") return json(route, { setupRequired: false });
    if (p === "/api/me") return json(route, me());
    if (p === "/api/profiles" && m === "GET") {
      return json(route, { profiles, current, canManage: current === 1, levels: LEVELS, max: 8 });
    }
    if ((mm = p.match(/^\/api\/profiles\/(\d+)\/switch$/)) && m === "POST") {
      const target = profiles.find((x) => x.id === Number(mm![1]));
      if (!target) return json(route, { error: "profile not found" }, 404);
      const fromMain = current === 1 && !target.isAccount;
      if (target.hasPin && target.id !== current && !fromMain && route.request().postDataJSON()?.pin !== "1234") {
        return json(route, { error: "wrong PIN" }, 403);
      }
      current = target.id;
      return json(route, me());
    }
    if ((mm = p.match(/^\/api\/profiles\/(\d+)$/)) && m === "PUT") {
      const target = profiles.find((x) => x.id === Number(mm![1]));
      if (!target) return json(route, { error: "profile not found" }, 404);
      const b = route.request().postDataJSON();
      Object.assign(target, { name: b.name ?? target.name, avatar: b.avatar ?? target.avatar, color: b.color ?? target.color });
      if (b.kids !== undefined) Object.assign(target, { kids: b.kids, maxAge: b.kids ? b.maxAge : undefined });
      if (b.pin !== undefined) target.hasPin = b.pin !== "";
      return json(route, { ok: true });
    }
    if (p === "/api/profiles" && m === "POST") {
      const b = route.request().postDataJSON();
      const np = { id: 10 + profiles.length, name: b.name, avatar: b.avatar || "", color: b.color || "", kids: !!b.kids,
        maxAge: b.kids ? b.maxAge : undefined, hasPin: !!b.pin, isAccount: false };
      profiles.push(np);
      return json(route, np);
    }
    if (p === "/api/libraries") return json(route, libs);
    if ((mm = p.match(/^\/api\/me\/libraries\/(\d+)$/)) && m === "PUT") {
      const l = libs.find((x) => x.id === Number(mm![1]));
      if (!l) return json(route, { error: "not found" }, 404);
      Object.assign(l, route.request().postDataJSON());
      return json(route, { ok: true });
    }
    if (p === "/api/home") return json(route, page.url().includes("extras=1") ? extras(home(st), st.noSuggest) : home(st));
    if ((mm = p.match(/^\/api\/items\/(\d+)\/suggestions\/hide$/)) && m === "POST") {
      const id = Number(mm[1]);
      st.noSuggest = [id, ...st.noSuggest.filter((x) => x !== id)];
      return json(route, { ok: true, id });
    }
    if (p === "/api/suggestions/hidden" && m === "GET") return json(route, { items: st.noSuggest.map((id) => byId.get(id)!) });
    if (p === "/api/suggestions/hidden" && m === "DELETE") {
      st.noSuggest = [];
      return json(route, { ok: true });
    }
    if ((mm = p.match(/^\/api\/suggestions\/hidden\/(\d+)$/)) && m === "DELETE") {
      st.noSuggest = st.noSuggest.filter((x) => x !== Number(mm![1]));
      return json(route, { ok: true });
    }
    if (p === "/api/wrapped") return json(route, wrapped(Number(u.searchParams.get("year")) || 2026));
    if ((mm = p.match(/^\/api\/items\/(\d+)\/continue\/hide$/)) && m === "POST") {
      st.hiddenContinue.add(Number(mm[1]));
      return json(route, { ok: true });
    }
    if ((mm = p.match(/^\/api\/items\/(\d+)\/watched$/)) && m === "POST" && route.request().postDataJSON()?.watched) st.seen.add(Number(mm[1]));
    if ((mm = p.match(/^\/api\/items\/(\d+)\/watchlist$/)) && m === "POST") {
      const id = Number(mm[1]);
      st.watchlist = st.watchlist.filter((x) => x !== id);
      if (route.request().postDataJSON()?.on) st.watchlist.unshift(id);
      return json(route, { ok: true });
    }
    if ((mm = p.match(/^\/api\/libraries\/(\d+)\/genres$/))) return json(route, GENRES.map((name, i) => ({ name, count: 40 - i * 5 })));
    if ((mm = p.match(/^\/api\/libraries\/(\d+)\/facets$/))) return json(route, facets(Number(mm[1]), withPrefs(u.searchParams)));
    if ((mm = p.match(/^\/api\/libraries\/(\d+)\/items$/))) return json(route, libraryItems(Number(mm[1]), withPrefs(u.searchParams)));
    // Picks (Reel Pick): Surprise me, the quiz, and what they remember.
    if ((mm = p.match(/^\/api\/libraries\/(\d+)\/picks$/))) {
      const all = pickable(u.searchParams);
      const fresh = all.filter((c) => !st.shown.has(c.id) && !st.hidden.has(c.id));
      return json(route, { picks: fresh.slice(0, 20).map((c) => pick(c, u.searchParams)), matching: all.length, fresh: fresh.length });
    }
    if ((mm = p.match(/^\/api\/libraries\/(\d+)\/quiz$/)) && m === "POST") {
      const a = route.request().postDataJSON() as { who?: string; exclude?: number[] };
      const q = new URLSearchParams(a.who === "family" ? [["content", "family"]] : []);
      const fresh = pickable(q).filter((c) => !st.shown.has(c.id) && !st.hidden.has(c.id) && !(a.exclude || []).includes(c.id));
      return json(route, { picks: fresh.slice(0, 15).map((c) => pick(c, q)), found: fresh.length });
    }
    if (p === "/api/picks/state") return json(route, { genres: st.genres, resting: st.shown.size, hidden: st.hidden.size, struck: st.skips.size, votes: st.votes.size });
    if (p === "/api/picks/genres" && m === "PUT") {
      st.genres = route.request().postDataJSON();
      return json(route, st.genres);
    }
    if (p === "/api/picks/feedback" && m === "POST") {
      const b = route.request().postDataJSON() as { ids: number[]; type: string };
      let hidden = false;
      for (const id of b.ids) {
        if (b.type === "shown") st.shown.add(id);
        if (b.type === "notint") st.hidden.add(id);
        if (b.type === "like" || b.type === "dislike") st.votes.set(id, b.type === "like" ? 1 : -1);
        if (b.type === "clear") st.votes.delete(id);
        if (b.type === "skip") {
          st.skips.set(id, (st.skips.get(id) || 0) + 1);
          if (st.skips.get(id)! >= 3) (st.hidden.add(id), (hidden = true));
        }
      }
      return json(route, { ok: true, hidden });
    }
    if (p === "/api/picks/reset" && m === "POST") {
      const what = route.request().postDataJSON()?.what;
      if (what !== "taste") (st.shown.clear(), st.hidden.clear(), st.skips.clear());
      if (what !== "rules") st.votes.clear();
      return json(route, { ok: true });
    }
    if ((mm = p.match(/^\/api\/items\/(\d+)\/mood$/))) {
      const id = Number(mm[1]);
      if (!movies.some((c) => c.id === id)) return json(route, { error: "not found" }, 404);
      const bars = [["v", "Mood", "Bleak", "Uplifting", 72], ["a", "Intensity", "Low-key", "Intense", 55], ["dark", "Tone", "Light", "Dark", 30], ["humour", "Humour", "Serious", "Funny", 64],
        ["complex", "Complexity", "Easy watch", "Demanding", 41], ["pace", "Pace", "Slow-burn", "Fast", 58], ["weight", "Tearjerker", "Dry-eyed", "Tissues", 22], ["comfort", "Comfort", "Challenging", "Comforting", 70]]
        .map(([key, label, low, high, value]) => ({ key, label, low, high, value }));
      return json(route, { bars, moods: ["Feel-good"], notes: ["No scares"], warnings: [], scored: true, vote: st.votes.get(id) || 0, hidden: st.hidden.has(id) });
    }
    if ((mm = p.match(/^\/api\/items\/(\d+)\/markers$/))) return json(route, []);
    // Only the first film has a trailer, so the other screens stay still.
    // Only one film has cast and "more like this", so other pages keep nothing below the fold.
    if ((mm = p.match(/^\/api\/items\/(\d+)\/extras$/))) {
      return json(route, Number(mm[1]) === ids.extras
        ? { cast: [{ id: 501, name: "Ada Reyes", character: "Mara", profile: "/p/ada.jpg" }, { id: 502, name: "Tom Okafor", character: "The Pilot" }], similar: movies.slice(21, 27) }
        : { cast: [], similar: [] });
    }
    // Ada is in three of your titles and two you don't have; Tom in nothing of yours.
    if ((mm = p.match(/^\/api\/people\/(\d+)$/))) {
      const ada = Number(mm[1]) === 501;
      return json(route, {
        person: ada
          ? { id: 501, name: "Ada Reyes", knownFor: "Acting", birthday: "1984-03-02", birthplace: "Lisbon, Portugal", profile: "/p/ada.jpg",
            biography: "Ada Reyes is an actor known for quiet, precise performances in thrillers and science fiction." }
          : { id: 502, name: "Tom Okafor", knownFor: "Acting" },
        inLibrary: ada ? [movies[20], movies[3], shows[1]] : [],
        roles: ada ? { [movies[20].id]: "Mara", [shows[1].id]: "Detective Lane" } : {},
        elsewhere: [
          { tmdbId: 9001, kind: "movie", title: "Glass Harbour", year: 2011, character: ada ? "June" : "Guard", poster: "/p/glass.jpg" },
          { tmdbId: 9002, kind: "show", title: "Night Desk", year: 2019, character: "Herself" },
        ],
      });
    }
    if ((mm = p.match(/^\/api\/items\/(\d+)\/guide$/))) return json(route, guide(Number(mm[1]), u.searchParams.get("season")));
    if ((mm = p.match(/^\/api\/items\/(\d+)\/trailer$/))) return json(route, { youtube: Number(mm[1]) === ids.movie ? "fakeTrailer" : "" });
    if ((mm = p.match(/^\/api\/play\/(\d+)$/))) {
      return json(route, { externalUrl: `http://media.test/stream/${mm[1]}`, position: 0, file: { id: Number(mm[1]), duration: 5400 }, audioOffsetMs: -120 });
    }
    if (p === "/api/screensaver") return json(route, [...movies.slice(0, 6), ...shows.slice(0, 2)]);
    if (p === "/api/activity") return json(route, { streams: [], recent: [] });
    if ((mm = p.match(/^\/api\/items\/(\d+)\/watched$/)) && m === "POST") return json(route, {});
    if ((mm = p.match(/^\/api\/items\/(\d+)$/))) {
      const c = byId.get(Number(mm[1]));
      return c ? json(route, { ...detail(c), watchlisted: st.watchlist.includes(c.id) }) : json(route, { error: "not found" }, 404);
    }
    if (p === "/api/search") {
      const q = (u.searchParams.get("q") || "").toLowerCase();
      const hit = (c: Card) => c.title.toLowerCase().includes(q) || !!c.showTitle?.toLowerCase().includes(q);
      // The search pages ask for part=titles and part=episodes separately.
      const part = u.searchParams.get("part");
      const titles = part !== "episodes", eps = part !== "titles";
      return json(route, { movies: titles ? movies.filter(hit).slice(0, 20) : [], shows: titles ? shows.filter(hit) : [],
        episodes: eps ? allEpisodes.filter(hit).slice(0, 20) : [],
        collections: titles && COLLECTION.card().name.toLowerCase().includes(q) ? [COLLECTION.card()] : [] });
    }
    if (p === "/api/share/targets") {
      return json(route, { targets: opts.share && me().isAdmin ? [{ id: "555", name: "Dad", plexUser: "bigdave", addedAt: 0 }] : [] });
    }
    if (p === "/api/share/watchlist" && m === "POST") {
      const c = byId.get(route.request().postDataJSON()?.itemId);
      return c ? json(route, { ok: true, already: false, title: c.title, kind: c.kind, name: "Dad" }) : json(route, { error: "nothing to share" }, 404);
    }
    if (p === "/api/history" && m === "GET") return json(route, history(st));
    if ((mm = p.match(/^\/api\/history\/(\d+)$/)) && m === "DELETE") {
      (st.historyHidden ||= new Set()).add(Number(mm[1]));
      return json(route, { ok: true });
    }
    if (p === "/api/watchlist") return json(route, { items: st.watchlist.map((id) => ({ ...byId.get(id)!, watchlisted: true })) });
    if (p === "/api/collections") return json(route, collectionsList());
    if ((mm = p.match(/^\/api\/collections\/(\d+)$/))) {
      if (Number(mm[1]) === 900) return json(route, { collection: COLLECTION.card(), items: COLLECTION.items() });
      const pc = PLEX_COLLECTIONS().find((c) => c.id === Number(mm![1]));
      if (pc) return json(route, { collection: pc, items: pc.kind === "show" ? shows.slice(0, 1) : movies.slice(0, 2) });
      return json(route, { error: "not found" }, 404);
    }
    if (p === "/api/ask") {
      // Plain-English search: a canned answer with picks and matches.
      const q = u.searchParams.get("q") || "";
      return json(route, { summary: `Results for ${q}`, engine: "claude", picks: movies.slice(0, 6), matches: [...movies.slice(6, 20), ...shows.slice(0, 4)] });
    }
    if (p === "/api/logout") return json(route, {});
    opts.unhandled?.push(`${m} ${p}`);
    return json(route, { error: "not mocked" }, 404);
  });
}
