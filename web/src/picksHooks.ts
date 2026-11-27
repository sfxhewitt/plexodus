// The "Surprise me" and "Help me choose" flows, shared by the phone and TV
// library pages: what is on screen, and what each button does to it.
import { useCallback, useEffect, useRef, useState } from "react";
import { useLocation } from "react-router-dom";
import { api } from "./api";
import { Pick, picks, QUIZ, QUIZ_SHOWN, QuizAnswers, quizStep } from "./picks";

/** Things changed that grids and Home show (watchlist flags, watched ticks). */
function touched() {
  api.invalidate("/api/home");
  api.invalidate("/api/libraries/");
  api.invalidate("/api/watchlist");
}

export async function pickWatchlist(p: Pick, on = true) {
  await api.post(`/api/items/${p.id}/watchlist`, { on });
  touched();
}
export async function pickSeen(p: Pick) {
  await api.post(`/api/items/${p.id}/watched`, { watched: true });
  touched();
}

// ---- coming back to the picks ----
//
// Opening a pick's page (or playing it, on a phone) leaves the library. What
// was on screen is kept here, and the library's place in the history is
// marked, so Back returns to the same picks rather than the bare library.
// Only Back does that: opening the library afresh starts clean.

type Kind = "surprise" | "quiz";
type Kept = {
  kind: Kind;
  libId: string;
  /** Surprise me only: the library filters it was drawn from. */
  filters?: string;
  /** TV: the button to land on when coming back. */
  focus?: string;
  surprise?: { queue: Pick[]; matching: number; held: number; shown: number[] };
  quiz?: { step: number; answers: QuizAnswers; pool: Pick[]; found: number; gone: number[]; marked: number[] };
};

const KEPT = "plexodus.picks.kept";
let kept: Kept | undefined;
let read = false;

function store(k: Kept | undefined) {
  kept = k;
  read = true;
  // Also in the tab's storage, so a reloaded page (a TV box short of memory) still comes back.
  try {
    if (k) sessionStorage.setItem(KEPT, JSON.stringify(k));
    else sessionStorage.removeItem(KEPT);
  } catch {
    /* in memory only */
  }
}

/** The picks left on screen, if they were this library's (and, for Surprise me, these filters'). */
export function keptPick(kind: Kind, libId: string | number, filters?: string): Kept | undefined {
  if (!read) {
    read = true;
    try {
      kept = JSON.parse(sessionStorage.getItem(KEPT) || "null") || undefined;
    } catch {
      kept = undefined;
    }
  }
  if (!kept || kept.kind !== kind || kept.libId !== String(libId)) return undefined;
  if (kind === "surprise" && (kept.filters || "") !== (filters || "")) return undefined;
  return kept;
}

// The mark goes straight into this history entry's state (where the router
// keeps `location.state`, under `usr`), not through a navigation: a replace
// would give the entry a new key, and the TV remembers focus by key.
function mark(kind: Kind | undefined) {
  const h = (window.history.state || {}) as { usr?: Record<string, unknown> | null };
  const { picking: _, ...usr } = h.usr || {};
  if (kind) usr.picking = kind;
  window.history.replaceState({ ...h, usr: Object.keys(usr).length ? usr : null }, "");
}

/**
 * For the buttons that leave the library from a pick: call just before
 * navigating. `focus` is the TV button to come back to.
 */
export function parkPick(focus?: string) {
  if (!kept) return;
  store({ ...kept, focus });
  mark(kept.kind);
}

/**
 * For the library page. `initial` is the overlay to reopen when Back has just
 * returned here from a pick's page; `resume` says the overlay should show what
 * it showed before; `end` is for when the overlay is closed for good.
 */
export function usePickResume(libId: string | undefined, filters: string): { initial: Kind | null; resume: boolean; end: () => void } {
  const marked = (useLocation().state as { picking?: Kind } | null)?.picking;
  const [initial] = useState<Kind | null>(() => (marked && libId && keptPick(marked, libId, filters) ? marked : null));
  const [resume, setResume] = useState(!!initial);
  const end = useCallback(() => {
    setResume(false);
    store(undefined);
    mark(undefined);
  }, []);
  return { initial, resume, end };
}

export type Surprise = {
  loading: boolean;
  error?: string;
  /** The film on screen, or undefined when there is nothing (left) to suggest. */
  cur?: Pick;
  /** How many more are lined up behind it. */
  left: number;
  /** Films that fit the filters but are resting or hidden. */
  held: number;
  matching: number;
  /** Pick another: counts as a skip of the one on screen. */
  another: () => void;
  /** Moves on without a skip (after Watchlist, Seen it or Not interested). */
  advance: () => void;
  notInterested: () => void;
};

/** One suggestion at a time from the library as it is filtered. */
export function useSurprise(libId: string | number, filters: string, say: (msg: string) => void, resume = false): Surprise {
  // Back from a pick's page: the same film, with the same ones lined up behind it.
  const [from] = useState(() => (resume ? keptPick("surprise", libId, filters)?.surprise : undefined));
  const [state, setState] = useState<{ loading: boolean; error?: string; queue: Pick[]; matching: number; held: number }>(
    from ? { loading: false, queue: from.queue, matching: from.matching, held: from.held } : { loading: true, queue: [], matching: 0, held: 0 });
  useEffect(() => {
    if (from) return;
    let alive = true;
    picks.surprise(libId, filters).then(
      (r) => alive && setState({ loading: false, queue: r.picks, matching: r.matching, held: r.matching - r.fresh }),
      (e: Error) => alive && setState({ loading: false, queue: [], matching: 0, held: 0, error: e.message }),
    );
    return () => {
      alive = false;
    };
  }, [libId, filters, from]);
  const cur = state.queue[0];
  // A film counts as suggested (and rests for 30 days) once it is on screen.
  const shown = useRef(new Set<number>(from?.shown));
  useEffect(() => {
    if (cur && !shown.current.has(cur.id)) {
      shown.current.add(cur.id);
      picks.feedback("shown", cur.id);
    }
  }, [cur]);
  useEffect(() => {
    if (!state.loading && !state.error)
      store({ kind: "surprise", libId: String(libId), filters, surprise: { queue: state.queue, matching: state.matching, held: state.held, shown: [...shown.current] } });
  }, [state, libId, filters]);
  const advance = useCallback(() => setState((s) => ({ ...s, queue: s.queue.slice(1) })), []);
  const another = useCallback(() => {
    if (cur) picks.feedback("skip", cur.id).then((r) => r.hidden && say(`Three skips: ${cur.title} is hidden for a year`));
    advance();
  }, [cur, advance, say]);
  const notInterested = useCallback(() => {
    if (cur) {
      picks.feedback("notint", cur.id);
      say(`${cur.title} is hidden for a year`);
    }
    advance();
  }, [cur, advance, say]);
  return { loading: state.loading, error: state.error, cur, left: Math.max(0, state.queue.length - 1), held: state.held, matching: state.matching, another, advance, notInterested };
}

export type Quiz = {
  /** Index into QUIZ, or QUIZ.length when the picks are showing. */
  step: number;
  answers: QuizAnswers;
  answer: (v: string) => void;
  next: () => void;
  back: () => void;
  loading: boolean;
  error?: string;
  /** Up to three picks on screen; the first is the top pick. */
  shown: Pick[];
  /** How many films fitted the answers at all. */
  found: number;
  /** Clears a pick from the results; the next one moves up. */
  dismiss: (p: Pick) => void;
  /** Three new picks; counts as a skip for the ones on screen. */
  shuffle: () => void;
  restart: () => void;
};

/** The quiz: answers, then three picks that backfill as they are dismissed. */
export function useQuiz(libId: string | number, resume = false): Quiz {
  // Back from a pick's page: the same answers and the same picks.
  const [from] = useState(() => (resume ? keptPick("quiz", libId)?.quiz : undefined));
  const [step, setStep] = useState(from?.step ?? 0);
  const [answers, setAnswers] = useState<QuizAnswers>(from?.answers ?? {});
  const [res, setRes] = useState<{ loading: boolean; error?: string; pool: Pick[]; found: number }>(
    { loading: false, pool: from?.pool ?? [], found: from?.found ?? 0 });
  // Dismissed or shuffled away: not offered again while the quiz is open.
  const gone = useRef<number[]>(from?.gone ? [...from.gone] : []);
  const run = useRef(0);

  const load = useCallback((a: QuizAnswers) => {
    const mine = ++run.current;
    setRes((r) => ({ ...r, loading: true, error: undefined }));
    picks.quiz(libId, a, gone.current).then(
      (r) => mine === run.current && setRes({ loading: false, pool: r.picks, found: r.found }),
      (e: Error) => mine === run.current && setRes({ loading: false, pool: [], found: 0, error: e.message }),
    );
  }, [libId]);

  const go = useCallback((from: number, dir: 1 | -1, a: QuizAnswers) => {
    const to = quizStep(from, dir, a);
    setStep(to);
    if (to >= QUIZ.length) load(a);
  }, [load]);

  const answer = useCallback((v: string) => {
    const k = QUIZ[step]?.k;
    if (!k || k === "genres") return;
    const a = { ...answers, [k]: v };
    setAnswers(a);
    go(step + 1, 1, a);
  }, [step, answers, go]);
  const next = useCallback(() => go(step + 1, 1, answers), [step, answers, go]);
  const back = useCallback(() => go(Math.min(step, QUIZ.length) - 1, -1, answers), [step, answers, go]);

  const shown = step >= QUIZ.length ? res.pool.slice(0, QUIZ_SHOWN) : [];
  const marked = useRef(new Set<number>(from?.marked));
  useEffect(() => {
    const fresh = shown.filter((p) => !marked.current.has(p.id)).map((p) => p.id);
    if (fresh.length) {
      fresh.forEach((id) => marked.current.add(id));
      picks.feedback("shown", ...fresh);
    }
  });
  useEffect(() => {
    if (!res.loading && !res.error)
      store({ kind: "quiz", libId: String(libId), quiz: { step, answers, pool: res.pool, found: res.found, gone: [...gone.current], marked: [...marked.current] } });
  }, [step, answers, res, libId]);

  const dismiss = useCallback((p: Pick) => {
    gone.current.push(p.id);
    setRes((r) => ({ ...r, pool: r.pool.filter((x) => x.id !== p.id) }));
  }, []);
  const shuffle = useCallback(() => {
    shown.forEach((p) => {
      gone.current.push(p.id);
      picks.feedback("skip", p.id);
    });
    load(answers);
  }, [shown, answers, load]);
  const restart = useCallback(() => {
    run.current++;
    setAnswers({});
    setRes({ loading: false, pool: [], found: 0 });
    setStep(0);
  }, []);

  return { step, answers, answer, next, back, loading: res.loading, error: res.error, shown, found: res.found, dismiss, shuffle, restart };
}
