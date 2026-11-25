// Reel Pick inside the film libraries: what the phone and TV library pages
// share. The server does the choosing (internal/api/picks.go); this is the
// vocabulary (moods, feel sliders, content limits, the quiz) and the calls.
import { api, Card } from "./api";

export const MOODS: { v: string; l: string; s: string }[] = [
  { v: "fg", l: "Feel-good", s: "Warm & uplifting" },
  { v: "th", l: "Thriller", s: "Edge of your seat" },
  { v: "cr", l: "Have a cry", s: "Bring tissues" },
  { v: "la", l: "Laugh", s: "Proper funny" },
  { v: "mb", l: "Mind-bender", s: "Twisty & clever" },
  { v: "ch", l: "Chill", s: "Cosy, low effort" },
  { v: "ad", l: "Adrenaline", s: "Big, loud, fast" },
];
export const moodLabel = (v: string) => MOODS.find((m) => m.v === v)?.l || v;

export const CONTENT: { v: string; l: string }[] = [
  { v: "noviolence", l: "No graphic violence" },
  { v: "noscares", l: "No scares" },
  { v: "family", l: "Family-friendly" },
  { v: "nosex", l: "No explicit sex" },
  { v: "romance", l: "Romance" },
];
export const contentLabel = (v: string) => CONTENT.find((m) => m.v === v)?.l || v;

/** The "feel" sliders: where a film should sit between two words, 0–100. */
export const FEEL: { k: string; l: string; lo: string; hi: string }[] = [
  { k: "v", l: "Mood", lo: "Bleak", hi: "Uplifting" },
  { k: "a", l: "Intensity", lo: "Low-key", hi: "Intense" },
  { k: "dark", l: "Tone", lo: "Light", hi: "Dark" },
  { k: "humour", l: "Humour", lo: "Serious", hi: "Funny" },
  { k: "complex", l: "Complexity", lo: "Easy watch", hi: "Demanding" },
  { k: "pace", l: "Pace", lo: "Slow-burn", hi: "Fast" },
  { k: "weight", l: "Tearjerker", lo: "Dry-eyed", hi: "Tissues" },
  { k: "comfort", l: "Comfort", lo: "Challenging", hi: "Comforting" },
];

/** "v:80,dark:20" ⇄ { v: 80, dark: 20 } */
export function parseFeel(s: string | null): Record<string, number> {
  const out: Record<string, number> = {};
  for (const part of (s || "").split(",")) {
    const [k, v] = part.split(":");
    const n = Number(v);
    if (FEEL.some((f) => f.k === k) && v !== undefined && v !== "" && n >= 0 && n <= 100) out[k] = n;
  }
  return out;
}
export const feelString = (f: Record<string, number>) => FEEL.filter((d) => f[d.k] !== undefined).map((d) => `${d.k}:${f[d.k]}`).join(",");
/** "Funny", "Fairly serious", "Humour: in between" */
export function feelWord(k: string, n: number) {
  const d = FEEL.find((f) => f.k === k);
  if (!d) return "";
  if (n >= 70) return d.hi;
  if (n <= 30) return d.lo;
  return n > 55 ? `Fairly ${d.hi.toLowerCase()}` : n < 45 ? `Fairly ${d.lo.toLowerCase()}` : `${d.l}: in between`;
}

/** Toggles one value in a comma-separated list. */
export function toggleIn(list: string | null, v: string) {
  const set = (list || "").split(",").filter(Boolean);
  return (set.includes(v) ? set.filter((x) => x !== v) : [...set, v]).join(",");
}

export interface Pick extends Card {
  why: string;
  moods: string[];
  genres: string[];
  blurb?: string;
}
export type PickList = { picks: Pick[]; matching: number; fresh: number };
export type GenrePrefs = { exclude: string[]; require: string[] };
export type PickState = { genres: GenrePrefs; resting: number; hidden: number; struck: number; votes: number };
export type ItemMood = {
  bars: { key: string; label: string; low: string; high: string; value: number }[];
  moods: string[]; notes: string[]; warnings: string[]; scored: boolean; vote: number; hidden: boolean;
};

export type Feedback = "shown" | "skip" | "notint" | "like" | "dislike" | "clear" | "unhide";

export const picks = {
  surprise: (libId: string | number, filters: string) => api.get<PickList>(`/api/libraries/${libId}/picks${filters ? `?${filters}` : ""}`),
  quiz: (libId: string | number, answers: QuizAnswers, exclude: number[]) =>
    api.post<{ picks: Pick[]; found: number }>(`/api/libraries/${libId}/quiz`, { ...answers, exclude }),
  feedback: (type: Feedback, ...ids: number[]) =>
    api.post<{ ok: boolean; strikes?: number; hidden?: boolean }>("/api/picks/feedback", { ids, type }).catch(() => ({ ok: false, strikes: undefined, hidden: undefined })),
  state: () => api.get<PickState>("/api/picks/state"),
  setGenres: (g: GenrePrefs) => api.put<GenrePrefs>("/api/picks/genres", g),
  reset: (what: "rules" | "taste" | "all") => api.post("/api/picks/reset", { what }),
  mood: (id: number) => api.get<ItemMood>(`/api/items/${id}/mood`),
};

/** A genre's saved state, and the next one a press moves it to: off → never → must → off. */
export function genreMode(g: GenrePrefs | undefined, name: string): 0 | 1 | 2 {
  return g?.exclude.includes(name) ? 1 : g?.require.includes(name) ? 2 : 0;
}
export function cycleGenre(g: GenrePrefs, name: string, to?: 0 | 1 | 2): GenrePrefs {
  const next = to ?? ((genreMode(g, name) + 1) % 3);
  const exclude = g.exclude.filter((x) => x !== name);
  const require = g.require.filter((x) => x !== name);
  if (next === 1) exclude.push(name);
  if (next === 2) require.push(name);
  return { exclude, require };
}

// ---- the quiz ----

export type QuizAnswers = { feel?: string; mode?: string; who?: string; time?: string; era?: string; taste?: string };
export type QuizStep = {
  k: keyof QuizAnswers | "genres";
  q: string;
  note?: string;
  opts?: { v: string; l: string; s?: string }[];
  /** Left out when an earlier answer makes it pointless. */
  skip?: (a: QuizAnswers) => boolean;
};

export const QUIZ: QuizStep[] = [
  { k: "feel", q: "How are you feeling right now?", opts: [
    { v: "stressed", l: "Stressed / wired" }, { v: "low", l: "Low / sad" }, { v: "bored", l: "Bored / flat" },
    { v: "happy", l: "Happy / buzzing" }, { v: "calm", l: "Calm / content" }, { v: "tired", l: "Tired" },
    { v: "", l: "Not sure", s: "Just find something good" },
  ] },
  { k: "mode", q: "Match your mood, or change it?", note: "People pick films to stay in a good mood or to shake off a bad one.",
    skip: (a) => !a.feel, opts: [
      { v: "match", l: "Match my mood", s: "Lean into how I feel" }, { v: "change", l: "Change my mood", s: "Shift me somewhere better" },
    ] },
  { k: "who", q: "Who's watching?", opts: [
    { v: "solo", l: "Just me", s: "My pick, my rules" }, { v: "date", l: "Date night", s: "Something for two" },
    { v: "family", l: "Family / kids", s: "Nothing too dark" }, { v: "friends", l: "Mates", s: "Crowd-pleaser" },
  ] },
  { k: "time", q: "How much time have you got?", opts: [
    { v: "short", l: "Under 90 mins", s: "Quick one" }, { v: "mid", l: "Up to 2 hours", s: "Standard film" },
    { v: "long", l: "All night", s: "Bring on the epic" }, { v: "", l: "Doesn't matter" },
  ] },
  { k: "era", q: "Old or new?", opts: [
    { v: "classic", l: "Classics", s: "Before 2000" }, { v: "mid", l: "2000s–2010s", s: "Modern-ish" },
    { v: "new", l: "Recent", s: "2018 onwards" }, { v: "", l: "Any era" },
  ] },
  { k: "genres", q: "Anything you never want to see?", note: "Press a genre to rule it out, again to insist on it, a third time to clear. Saved for this library and every pick." },
  { k: "taste", q: "Last one: safe bet or something different?", opts: [
    { v: "classic", l: "Safe bet", s: "Highly rated, proven" }, { v: "gem", l: "Something different", s: "Less obvious picks" }, { v: "", l: "Either" },
  ] },
];

/** The next step to ask from `from` in direction `dir`, or QUIZ.length for the results. */
export function quizStep(from: number, dir: 1 | -1, a: QuizAnswers) {
  let i = from;
  while (i >= 0 && i < QUIZ.length && QUIZ[i].skip?.(a)) i += dir;
  return Math.max(0, i);
}

export const QUIZ_SHOWN = 3;

/** "2h 05m · 2014 · Drama, Sci-Fi" */
export function pickMeta(p: Pick) {
  const run = p.runtime ? `${Math.floor(p.runtime / 60)}h ${String(p.runtime % 60).padStart(2, "0")}m` : "";
  return [p.year || "", run, p.genres.slice(0, 3).join(", ")].filter(Boolean).join(" · ");
}
