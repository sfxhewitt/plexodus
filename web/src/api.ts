export type Kind = "movie" | "show" | "season" | "episode";

export interface Card {
  id: number;
  libraryId: number;
  kind: Kind;
  title: string;
  year?: number;
  poster?: string;
  backdrop?: string;
  logo?: string;
  rating?: number;
  /** Rotten Tomatoes critics' Tomatometer and audience score, 0–100. */
  rtCritic?: number;
  rtAudience?: number;
  runtime?: number;
  seasonNumber?: number;
  episodeNumber?: number;
  parentId?: number;
  showId?: number;
  showTitle?: string;
  showPoster?: string;
  showBackdrop?: string;
  addedAt: number;
  position?: number;
  duration?: number;
  watched?: boolean;
  leafCount?: number;
  viewedLeafCount?: number;
  overview?: string;
  /** On the signed-in user's watchlist. */
  watchlisted?: boolean;
  /** Smart rows: replaces the subtitle ("Season 2 · 3 left · 2h 10m"). */
  note?: string;
  /** How well it fits the mood asked for, 40–99, on a Best match list. */
  match?: number;
}

export interface Stream {
  index: number;
  type: "video" | "audio" | "subtitle";
  codec: string;
  language?: string;
  title?: string;
  channels?: number;
  default?: boolean;
  forced?: boolean;
}

export interface MediaFile {
  id: number;
  path: string;
  name: string;
  size: number;
  container?: string;
  videoCodec?: string;
  audioCodec?: string;
  width?: number;
  height?: number;
  duration?: number;
  bitrate?: number;
  streams?: Stream[];
  probed: boolean;
  probeError?: string;
}

export interface Detail extends Card {
  originalTitle?: string;
  tagline?: string;
  genres: string[];
  releaseDate?: string;
  tmdbId?: number;
  imdbId?: string;
  metaState: string;
  files: MediaFile[];
  children: Card[];
  parent?: Card;
  show?: Card;
  onDeck?: Card;
  siblings?: Card[];
  collection?: Collection;
  collectionItems?: Card[];
}

/** A collection: a TMDB franchise ("The Matrix Collection") or any Plex collection, of films or shows. */
export interface Collection {
  id: number;
  name: string;
  poster?: string;
  backdrop?: string;
  count: number;
  watched: number;
  source?: "tmdb" | "plex";
  kind?: "movie" | "show";
  summary?: string;
  group?: string;
}

/** One group of collections on the Collections page ("Franchises & series"). */
export interface CollectionShelf {
  name: string;
  collections: Collection[];
}

/** "3 films", "1 show". */
export function collectionCount(c: Collection, n = c.count) {
  const word = c.kind === "show" ? "show" : "film";
  return `${n.toLocaleString()} ${word}${n === 1 ? "" : "s"}`;
}

export interface Library {
  id: number;
  name: string;
  kind: "movies" | "shows";
  paths: string[];
  trustDirMtime: boolean;
  lastScanAt?: number;
  count: number;
  fileCount: number;
  unprobed: number;
  pendingMetadata: number;
  totalSize: number;
  backdrop?: string;
  /** The signed-in user hid it from the menu / from Home. */
  hidden?: boolean;
  hiddenHome?: boolean;
}

export type LibraryPrefs = Pick<Library, "hidden" | "hiddenHome">;

/** Saves the signed-in user's display choices for a library and tells open views. */
export async function setLibraryPrefs(id: number, prefs: LibraryPrefs) {
  await api.put(`/api/me/libraries/${id}`, prefs);
  api.invalidate("/api/libraries");
  api.invalidate("/api/home");
  window.dispatchEvent(new CustomEvent("plexodus:library-prefs", { detail: { id, prefs } }));
}

export interface User {
  /** The active viewer: the account, or one of its profiles. */
  id: number;
  username: string;
  isAdmin: boolean;
  allLibraries: boolean;
  libraries: number[];
  accountId?: number;
  isProfile?: boolean;
  /** Profile name (the username for the main profile). */
  name?: string;
  avatar?: string;
  color?: string;
  /** Kids profiles: titles rated above this age are hidden. */
  maxAge?: number;
  kids?: boolean;
}

/** A row in Settings > Users. */
export interface AdminUser extends User {
  /** Set for accounts linked to plex.tv (Sign in with Plex). */
  plexUsername?: string;
  /** A Plex friend whose libraries follow their Plex share. */
  followPlex: boolean;
  /** Last sign-in (unix seconds). */
  lastSeen?: number;
}

/** One live stream on the Now watching page. */
export interface NowPlaying {
  key: string;
  userId: number;
  username: string;
  profile?: string;
  itemId: number;
  kind: string;
  title: string;
  year?: number;
  showTitle?: string;
  seasonNumber?: number;
  episodeNumber?: number;
  poster?: string;
  backdrop?: string;
  library?: string;
  state: "playing" | "paused";
  position: number;
  duration: number;
  method: "direct" | "remux";
  device: string;
  app: string;
  ip: string;
  local: boolean;
  startedAt: number;
  container?: string;
  videoCodec?: string;
  audioCodec?: string;
  width?: number;
  height?: number;
  bitrate?: number;
  bytesServed: number;
}

export interface RecentPlay {
  username: string;
  profile?: string;
  itemId: number;
  kind: string;
  title: string;
  showTitle?: string;
  seasonNumber?: number;
  episodeNumber?: number;
  poster?: string;
  at: number;
  source: string;
}

export interface Activity {
  streams: NowPlaying[];
  recent?: RecentPlay[];
}

/** Matches /invite/<link token or typed code>. */
export const inviteFromPath = (path: string) => path.match(/^\/invite\/([A-Za-z0-9-]{8,64})\/?$/)?.[1];

export interface Invite {
  token: string;
  /** "K7QM-2XPA": typed into the app instead of a server address. */
  code?: string;
  /** 0 = any number of people (e.g. an app store reviewer's code). */
  maxUses: number;
  uses: number;
  note?: string;
  allLibraries: boolean;
  libraries: number[];
  createdAt: number;
  expiresAt: number;
  usedBy?: string;
}

export interface Row {
  title: string;
  link?: string;
  kind: "poster" | "landscape";
  items: Card[];
  /** A "Tonight" row picked for the user (season, because, short, gems). */
  smart?: string;
}

export interface HomeData {
  hero: Card[];
  rows: Row[];
  /** Present when the user has plays this year: Home offers their Wrapped. */
  wrapped?: { year: number; plays: number };
  /** How many titles the viewer has removed from their suggestions. */
  removed?: number;
}

/** "Remove from suggestions": the title stops being suggested on Home. */
export const suggestions = {
  hide: (id: number) => api.post(`/api/items/${id}/suggestions/hide`),
  restore: (id: number) => api.del(`/api/suggestions/hidden/${id}`),
  restoreAll: () => api.del("/api/suggestions/hidden"),
};

export interface WrappedTitle {
  card: Card;
  plays: number;
  seconds: number;
}

export interface Wrapped {
  year: number;
  years: number[];
  plays: number;
  movies: number;
  episodes: number;
  shows: number;
  seconds: number;
  days: number;
  topShows: WrappedTitle[];
  topMovies: WrappedTitle[];
  topGenres: { name: string; seconds: number }[];
  hours: number[];
  weekdays: number[];
  months: number[];
  peakHour: number;
  persona: string;
  blurb: string;
  streak: { days: number; start?: string; end?: string };
  binge?: { show: Card; episodes: number; date: string; seconds: number };
  biggestDay?: { date: string; plays: number; seconds: number };
  first?: { card: Card; at: number };
}

export function wrappedUrl(year?: number) {
  let tz = "";
  try {
    tz = Intl.DateTimeFormat().resolvedOptions().timeZone || "";
  } catch {
    /* old WebView */
  }
  return `/api/wrapped?tz=${encodeURIComponent(tz)}${year ? `&year=${year}` : ""}`;
}

export interface PlayInfo {
  itemId: number;
  file: MediaFile;
  fileName: string;
  mime: string;
  streamUrl: string;
  externalUrl: string;
  position: number;
  /** The server has ffmpeg, so files a browser can't play can be remuxed. */
  hls: boolean;
  /** Saved lip sync fix for this file in ms (positive = sound later). */
  audioOffsetMs?: number;
}

export interface ScanProgress {
  libraryId: number;
  started: string;
  finished?: string;
  running: boolean;
  dirs: number;
  dirsSkipped: number;
  files: number;
  added: number;
  updated: number;
  removed: number;
  error?: string;
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

/** Turns server and storage errors into something a person on a sofa can act on. */
export function friendly(status: number, msg: string) {
  const m = msg.toLowerCase();
  if (/database is (locked|busy)|sqlite_busy/.test(m)) return "The server is busy right now. Try again in a moment.";
  if (/input\/output error|transport endpoint|stale file handle|no such device|rclone/.test(m)) {
    return "The drive holding this file isn't answering. It may be waking up, so try again in a minute.";
  }
  if (status === 502 || status === 503 || status === 504) return "The server is restarting or busy. Trying again usually works.";
  if (status >= 500 && /^(internal server error|bad gateway)?$/.test(m)) return "Something went wrong on the server. Try again in a moment.";
  return msg;
}

/**
 * How long a GET may take before it gives up. Without a limit a request the
 * server never answers leaves a page spinning forever; with one the page
 * shows an error and a way to try again.
 */
const GET_TIMEOUT_MS = 45_000;

async function request<T>(method: string, url: string, body?: unknown): Promise<T> {
  const ctl = method === "GET" && typeof AbortController !== "undefined" ? new AbortController() : null;
  const timer = ctl ? setTimeout(() => ctl.abort(), GET_TIMEOUT_MS) : undefined;
  let res: Response;
  try {
    res = await fetch(url, {
      method,
      credentials: "same-origin",
      headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
      body: body !== undefined ? JSON.stringify(body) : undefined,
      signal: ctl?.signal,
    });
  } catch (e) {
    if (ctl?.signal.aborted) throw new ApiError(0, "The server is taking too long to answer.");
    throw new ApiError(0, "Can't reach your Plexodus server. Check it's running and this device is online.");
  } finally {
    clearTimeout(timer);
  }
  const data = await res.json().catch(() => ({}));
  // The server gave its previous Home while a fresh one is built (just
  // restarted, or a change is still being applied): Home asks again shortly.
  if (res.ok && url.startsWith("/api/home")) {
    window.dispatchEvent(new CustomEvent("plexodus:home-stale", { detail: !!res.headers.get("X-Home-Stale") }));
  }
  if (!res.ok) {
    if (res.status === 401 && !url.startsWith("/api/login")) window.dispatchEvent(new Event("plexodus:unauthorized"));
    throw new ApiError(res.status, friendly(res.status, (data as { error?: string }).error || res.statusText));
  }
  return data as T;
}

// Tiny in-memory cache so navigating back is instant; entries are
// revalidated in the background by useApi.
const cache = new Map<string, unknown>();

export const api = {
  get: <T,>(url: string) =>
    request<T>("GET", url).then((d) => {
      cache.set(url, d);
      return d;
    }),
  cached: <T,>(url: string) => cache.get(url) as T | undefined,
  invalidate: (prefix = "") => {
    for (const k of [...cache.keys()]) if (k.startsWith(prefix)) cache.delete(k);
  },
  post: <T,>(url: string, body: unknown = {}) => request<T>("POST", url, body),
  put: <T,>(url: string, body: unknown = {}) => request<T>("PUT", url, body),
  del: <T,>(url: string) => request<T>("DELETE", url),
};

export function art(path: string | undefined, size: "w185" | "w342" | "w500" | "w780" | "w1280" | "original") {
  if (!path) return undefined;
  if (path.startsWith("plex:")) return `/art/plex/${size}?p=${encodeURIComponent(path.slice(5))}`;
  return `/art/${size}${path}`;
}

/** A title logo. Plex ones are asked for as PNG so they keep their see-through background. */
export function logoArt(path: string | undefined) {
  const u = art(path, "w500");
  return u && path!.startsWith("plex:") ? `${u}&f=png` : u;
}

export function fmtRuntime(min?: number) {
  if (!min) return "";
  const h = Math.floor(min / 60);
  const m = min % 60;
  return h ? `${h}h ${m}m` : `${m}m`;
}

export function fmtTime(sec: number) {
  if (!isFinite(sec) || sec < 0) sec = 0;
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = Math.floor(sec % 60);
  const mm = h ? String(m).padStart(2, "0") : String(m);
  return `${h ? h + ":" : ""}${mm}:${String(s).padStart(2, "0")}`;
}

export function fmtSize(bytes: number) {
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (bytes >= 1024 && i < u.length - 1) {
    bytes /= 1024;
    i++;
  }
  return `${bytes.toFixed(i >= 3 ? 2 : 0)} ${u[i]}`;
}

export function resolution(f: MediaFile) {
  const w = f.width || 0;
  const h = f.height || 0;
  if (w >= 3200 || h >= 2000) return "4K";
  if (w >= 1800 || h >= 1000) return "1080p";
  if (w >= 1200 || h >= 700) return "720p";
  if (h) return `${h}p`;
  return "";
}

export function episodeLabel(c: Card) {
  if (c.seasonNumber == null || c.episodeNumber == null) return "";
  return `S${c.seasonNumber} · E${c.episodeNumber}`;
}

/** A film or episode on History, with when it was last played (unix seconds). */
export interface HistoryCard extends Card {
  playedAt: number;
}

export interface HistoryPage {
  items: HistoryCard[];
  more: boolean;
  /** Pass as ?before= for the next (older) page. */
  before: number;
}

/** History split into days, newest first: "Today", "Yesterday", "Monday", "Mon 3 Nov". */
export function historyDays(items: HistoryCard[], now = new Date()) {
  const startOf = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const today = startOf(now);
  const days: { label: string; items: HistoryCard[] }[] = [];
  for (const c of items) {
    const d = new Date(c.playedAt * 1000);
    const ago = Math.round((today - startOf(d)) / 86400000);
    const label = ago <= 0 ? "Today" : ago === 1 ? "Yesterday"
      : ago < 7 ? d.toLocaleDateString(undefined, { weekday: "long" })
      : d.toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "short", ...(d.getFullYear() !== now.getFullYear() ? { year: "numeric" } : {}) });
    const last = days[days.length - 1];
    if (last && last.label === label) last.items.push(c);
    else days.push({ label, items: [c] });
  }
  return days;
}

/** "14:02 · 12m of 1h 52m" / "14:02 · Watched": when and how far, for a History entry. */
export function historyNote(c: HistoryCard) {
  const at = new Date(c.playedAt * 1000).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  let where = "";
  if (c.watched && !c.position) where = "Watched";
  else if (c.position && c.duration) where = `${fmtRuntime(Math.max(1, Math.round(c.position / 60)))} of ${fmtRuntime(Math.round(c.duration / 60))}`;
  else if (c.position) where = `${fmtRuntime(Math.max(1, Math.round(c.position / 60)))} in`;
  return [at, where].filter(Boolean).join(" · ");
}

export interface Marker {
  type: "intro" | "credits" | "recap";
  start: number;
  end: number;
}

export interface PlexSyncStatus {
  enabled: boolean;
  running: boolean;
  lastRun?: string;
  lastOk?: string;
  checked: number;
  updated: number;
  error?: string;
  intervalMinutes: number;
  /** What the last watch sync cost. */
  tookMs: number;
  requests: number;
  /** Markers and collections copy in the background, apart from the watch sync. */
  extras: {
    running: boolean;
    phase?: string;
    error?: string;
    collectionsAt?: string;
    collectionsTookSeconds?: number;
    /** The last copy of the owner's Plex Watchlist. */
    watchlist?: {
      at?: number;
      onPlex: number;
      copied: number;
      added: number;
      /** On the Plex Watchlist but not in the library yet. */
      waiting?: string[];
      error?: string;
    };
  };
}

export interface PlexStatus {
  running: boolean;
  phase: string;
  section?: string;
  items: number;
  files: number;
  watched: number;
  markers: number;
  started: string;
  finished?: string;
  error?: string;
}

/** GET /api/ask: a plain-English request turned into library results. */
export interface AskResult {
  summary: string;
  /** "claude" when Claude translated the request, "basic" for the built-in parser. */
  engine: "claude" | "basic" | "title";
  notice?: string;
  /** Claude's suggestions that are in the library, best first. */
  picks: Card[];
  /** Everything else matching the filters. */
  matches: Card[];
}

/** True when a search reads like a request rather than a title. */
export const looksLikeQuestion = (q: string) => q.trim().split(/\s+/).length >= 3 || /\b(like|similar|haven'?t|under|from the|[0-9]0'?s)\b/i.test(q);

/** Someone whose Plex Watchlist Share can add to (only admins get any). */
export type ShareTarget = { id: string; name: string; plexUser: string };

/** Puts a film, or the show an episode/season is from, on their Plex Watchlist; returns what to tell the user. */
export async function shareToPlex(t: ShareTarget, ids: { itemId?: number; fileId?: number }): Promise<string> {
  const r = await api.post<{ already: boolean; title: string }>("/api/share/watchlist", { target: t.id, ...ids });
  return r.already ? `${r.title} is already on ${t.name}'s Plex watchlist` : `Added ${r.title} to ${t.name}'s Plex watchlist`;
}
