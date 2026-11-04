// Settings and shared state for the TV extras: the idle screensaver and
// trailers behind the art. Settings are per device (a TV in the bedroom can
// differ from the one in the lounge), so they live in localStorage.
import { native } from "../native";

export interface AmbientPrefs {
  /** Minutes idle before the screensaver; 0 turns it off. */
  screensaver: number;
  trailers: boolean;
  trailerSound: boolean;
}

const KEY = "plexodus.tv.ambient";
const DEFAULTS: AmbientPrefs = { screensaver: 3, trailers: true, trailerSound: true };

let prefs: AmbientPrefs = DEFAULTS;
try {
  prefs = { ...DEFAULTS, ...JSON.parse(localStorage.getItem(KEY) || "{}") };
} catch {
  /* private mode or bad JSON: defaults */
}

const prefListeners = new Set<() => void>();
export const ambientPrefs = () => prefs;
export function setAmbientPrefs(p: Partial<AmbientPrefs>) {
  prefs = { ...prefs, ...p };
  try {
    localStorage.setItem(KEY, JSON.stringify(prefs));
  } catch {
    /* ignore */
  }
  prefListeners.forEach((l) => l());
}
export const onAmbientPrefs = (l: () => void) => {
  prefListeners.add(l);
  return () => void prefListeners.delete(l);
};

/**
 * Whether trailers can play here. Older Android app builds would hand a
 * trailer frame's navigation to the YouTube app, so they need the update.
 */
export function trailersSupported() {
  if (!native) return true;
  try {
    return !!native.canEmbed?.();
  } catch {
    return false;
  }
}

// ---------- the backdrop's trailer ----------

// Pages ask for a title's trailer once you've rested on it; the shared
// backdrop plays it. A tiny store rather than React state, so asking doesn't
// re-render the page (or the app shell) on every move.
let trailerId: number | undefined;
let timer: ReturnType<typeof setTimeout> | undefined;
const trailerListeners = new Set<(id: number | undefined) => void>();
const emit = (id: number | undefined) => {
  if (id === trailerId) return;
  trailerId = id;
  trailerListeners.forEach((l) => l(id));
};

/** Plays this title's trailer behind the page after `delay` ms; undefined stops it now. */
export function requestTrailer(id: number | undefined, delay = 2500) {
  clearTimeout(timer);
  if (id !== trailerId) emit(undefined);
  if (id === undefined || !prefs.trailers || !trailersSupported()) return;
  timer = setTimeout(() => emit(id), delay);
}
export const stopTrailer = () => requestTrailer(undefined);
export const onTrailer = (l: (id: number | undefined) => void) => {
  trailerListeners.add(l);
  return () => void trailerListeners.delete(l);
};

// ---------- screensaver trigger ----------

const saverListeners = new Set<() => void>();
/** Starts the screensaver now (Settings' preview). */
export const startScreensaver = () => saverListeners.forEach((l) => l());
export const onStartScreensaver = (l: () => void) => {
  saverListeners.add(l);
  return () => void saverListeners.delete(l);
};
