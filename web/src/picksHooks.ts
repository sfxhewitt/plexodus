// The "Surprise me" and "Help me choose" flows, shared by the phone and TV
// library pages: what is on screen, and what each button does to it.
import { useCallback, useEffect, useRef, useState } from "react";
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
export function useSurprise(libId: string | number, filters: string, say: (msg: string) => void): Surprise {
  const [state, setState] = useState<{ loading: boolean; error?: string; queue: Pick[]; matching: number; held: number }>(
    { loading: true, queue: [], matching: 0, held: 0 });
  useEffect(() => {
    let alive = true;
    picks.surprise(libId, filters).then(
      (r) => alive && setState({ loading: false, queue: r.picks, matching: r.matching, held: r.matching - r.fresh }),
      (e: Error) => alive && setState({ loading: false, queue: [], matching: 0, held: 0, error: e.message }),
    );
    return () => {
      alive = false;
    };
  }, [libId, filters]);
  const cur = state.queue[0];
  // A film counts as suggested (and rests for 30 days) once it is on screen.
  const shown = useRef(new Set<number>());
  useEffect(() => {
    if (cur && !shown.current.has(cur.id)) {
      shown.current.add(cur.id);
      picks.feedback("shown", cur.id);
    }
  }, [cur]);
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
export function useQuiz(libId: string | number): Quiz {
  const [step, setStep] = useState(0);
  const [answers, setAnswers] = useState<QuizAnswers>({});
  const [res, setRes] = useState<{ loading: boolean; error?: string; pool: Pick[]; found: number }>({ loading: false, pool: [], found: 0 });
  // Dismissed or shuffled away: not offered again while the quiz is open.
  const gone = useRef<number[]>([]);
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
  const marked = useRef(new Set<number>());
  useEffect(() => {
    const fresh = shown.filter((p) => !marked.current.has(p.id)).map((p) => p.id);
    if (fresh.length) {
      fresh.forEach((id) => marked.current.add(id));
      picks.feedback("shown", ...fresh);
    }
  });

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
