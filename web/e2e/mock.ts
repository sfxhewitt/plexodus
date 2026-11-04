// A small fake Plexodus library served through Playwright's request routing,
// so the TV UI renders the same screens every run without a real server.
// Shapes follow web/src/api.ts.
import type { Page, Route } from "@playwright/test";
import type { Card, Detail, Library, Row } from "../src/api";

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
    overview: `${title} is a film about people, places and the long way round. Placeholder synopsis for the test library.`,
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
    overview: `${title}: a series. Placeholder synopsis for the test library.`,
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

/** Well-known ids the specs navigate to. */
export const ids = {
  movie: movies[0].id,
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

function detail(c: Card): Detail {
  const base: Detail = { ...c, genres: GENRES.slice(c.id % 3, (c.id % 3) + 3), metaState: "ok", files: [], children: [], tagline: "Every map ends somewhere." };
  if (c.kind === "movie") return { ...base, files: [file(c)] };
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
  watchlist: number[];
}

function home(st: UserState = { hiddenContinue: new Set(), watchlist: [] }): { hero: Card[]; rows: Row[] } {
  const continuing = [...movies, ...allEpisodes].filter((c) => c.position && !st.hiddenContinue.has(c.id));
  const wl = st.watchlist.map((id) => ({ ...byId.get(id)!, watchlisted: true }));
  const onDeck = shows.slice(1, 6).map((s) => episodes.get(seasons.get(s.id)![0].id)![0]);
  return {
    hero: movies.slice(0, 5),
    rows: [
      { title: "Continue Watching", kind: "landscape", items: continuing },
      ...(wl.length ? [{ title: "Watchlist", kind: "poster" as const, items: wl }] : []),
      { title: "On Deck", kind: "landscape", items: onDeck },
      { title: "Recently Added Movies", kind: "poster", link: `/library/${MOVIES_LIB}?sort=added`, items: movies.slice(0, 20) },
      { title: "Recently Added TV", kind: "poster", link: `/library/${SHOWS_LIB}?sort=added`, items: shows },
      { title: "Top Rated", kind: "poster", items: [...movies].sort((a, b) => (b.rating || 0) - (a.rating || 0)).slice(0, 15) },
    ],
  };
}

function libraryItems(id: number, q: URLSearchParams) {
  let items = id === MOVIES_LIB ? [...movies] : id === SHOWS_LIB ? [...shows] : [];
  const genre = q.get("genre");
  if (genre) items = items.filter((c) => detail(c).genres.includes(genre));
  if (q.get("filter") === "unwatched") items = items.filter((c) => !c.watched);
  if (q.get("filter") === "inprogress") items = items.filter((c) => c.position);
  const sort = q.get("sort") || "title";
  const key = (c: Card) => (sort === "added" ? c.addedAt : sort === "rating" ? c.rating || 0 : sort === "year" || sort === "release" ? c.year || 0 : c.title);
  items.sort((a, b) => (key(a) < key(b) ? -1 : key(a) > key(b) ? 1 : 0));
  if ((q.get("order") || (sort === "title" ? "asc" : "desc")) === "desc") items.reverse();
  const offset = Number(q.get("offset") || 0);
  const limit = Number(q.get("limit") || 120);
  return { items: items.slice(offset, offset + limit), total: items.length, offset };
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
}

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
  if (m.event !== "listening") return;
  parent.postMessage(JSON.stringify({ event: "onReady", id: 1 }), "*");
  setTimeout(function () { parent.postMessage(JSON.stringify({ event: "onStateChange", info: 1, id: 1 }), "*"); }, 100);
});
</script></body>`,
    }),
  );

  // Per-test copy: the settings screen changes library choices.
  const libs = libraries.map((l) => ({ ...l, hidden: false, hiddenHome: false }));
  const st: UserState = { hiddenContinue: new Set(), watchlist: [] };

  await page.route("**/api/**", (route) => {
    const u = new URL(route.request().url());
    const p = u.pathname;
    const m = route.request().method();
    let mm: RegExpMatchArray | null;
    if (p === "/api/status") return json(route, { setupRequired: false });
    if (p === "/api/me") return json(route, { id: 1, username: "daniel", isAdmin: true, allLibraries: true, libraries: [] });
    if (p === "/api/libraries") return json(route, libs);
    if ((mm = p.match(/^\/api\/me\/libraries\/(\d+)$/)) && m === "PUT") {
      const l = libs.find((x) => x.id === Number(mm![1]));
      if (!l) return json(route, { error: "not found" }, 404);
      Object.assign(l, route.request().postDataJSON());
      return json(route, { ok: true });
    }
    if (p === "/api/home") return json(route, home(st));
    if ((mm = p.match(/^\/api\/items\/(\d+)\/continue\/hide$/)) && m === "POST") {
      st.hiddenContinue.add(Number(mm[1]));
      return json(route, { ok: true });
    }
    if ((mm = p.match(/^\/api\/items\/(\d+)\/watchlist$/)) && m === "POST") {
      const id = Number(mm[1]);
      st.watchlist = st.watchlist.filter((x) => x !== id);
      if (route.request().postDataJSON()?.on) st.watchlist.unshift(id);
      return json(route, { ok: true });
    }
    if ((mm = p.match(/^\/api\/libraries\/(\d+)\/genres$/))) return json(route, GENRES.map((name, i) => ({ name, count: 40 - i * 5 })));
    if ((mm = p.match(/^\/api\/libraries\/(\d+)\/items$/))) return json(route, libraryItems(Number(mm[1]), u.searchParams));
    if ((mm = p.match(/^\/api\/items\/(\d+)\/markers$/))) return json(route, []);
    // Only the first film has a trailer, so the other screens stay still.
    if ((mm = p.match(/^\/api\/items\/(\d+)\/trailer$/))) return json(route, { youtube: Number(mm[1]) === ids.movie ? "fakeTrailer" : "" });
    if ((mm = p.match(/^\/api\/play\/(\d+)$/))) {
      return json(route, { externalUrl: `http://media.test/stream/${mm[1]}`, position: 0, file: { id: Number(mm[1]), duration: 5400 } });
    }
    if (p === "/api/screensaver") return json(route, [...movies.slice(0, 6), ...shows.slice(0, 2)]);
    if ((mm = p.match(/^\/api\/items\/(\d+)\/watched$/)) && m === "POST") return json(route, {});
    if ((mm = p.match(/^\/api\/items\/(\d+)$/))) {
      const c = byId.get(Number(mm[1]));
      return c ? json(route, { ...detail(c), watchlisted: st.watchlist.includes(c.id) }) : json(route, { error: "not found" }, 404);
    }
    if (p === "/api/search") {
      const q = (u.searchParams.get("q") || "").toLowerCase();
      const hit = (c: Card) => c.title.toLowerCase().includes(q) || !!c.showTitle?.toLowerCase().includes(q);
      return json(route, { movies: movies.filter(hit).slice(0, 20), shows: shows.filter(hit), episodes: allEpisodes.filter(hit).slice(0, 20) });
    }
    if (p === "/api/logout") return json(route, {});
    opts.unhandled?.push(`${m} ${p}`);
    return json(route, { error: "not mocked" }, 404);
  });
}
