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
}

export interface User {
  id: number;
  username: string;
  isAdmin: boolean;
  allLibraries: boolean;
  libraries: number[];
}

export interface Invite {
  token: string;
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

async function request<T>(method: string, url: string, body?: unknown): Promise<T> {
  const res = await fetch(url, {
    method,
    credentials: "same-origin",
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    if (res.status === 401 && !url.startsWith("/api/login")) window.dispatchEvent(new Event("plexodus:unauthorized"));
    throw new ApiError(res.status, (data as { error?: string }).error || res.statusText);
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

export interface Marker {
  type: "intro" | "credits";
  start: number;
  end: number;
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
