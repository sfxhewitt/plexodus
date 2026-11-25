// "Surprise me" and "Help me choose" on the TV: Reel Pick's picker and quiz,
// as full-screen overlays over a film library. The flows live in
// ../picksHooks; this is how they look and move with a remote.
import { ReactNode, useCallback, useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { useNavigate } from "react-router-dom";
import { api, art } from "../api";
import { Check, Dice, Info, Play, Plus, Refresh, ThumbDown, ThumbUp, X } from "../components/Icons";
import { cycleGenre, GenrePrefs, genreMode, Pick, pickMeta, picks, QUIZ } from "../picks";
import { pickSeen, pickWatchlist, useQuiz, useSurprise } from "../picksHooks";
import { Art, Spinner, TvButton } from "./components";
import { focusInitial, focusKey, onBack } from "./focus";
import { useToast } from "./hooks";
import { playItem } from "./play";

function Overlay({ className, onClose, children }: { className: string; onClose: () => void; children: ReactNode }) {
  useEffect(() => onBack(() => (onClose(), true)), [onClose]);
  return createPortal(<div className={`tv-modal tv-pick-wrap ${className}`} data-scope>{children}</div>, document.body);
}

function Why({ p }: { p: Pick }) {
  return p.why ? <p className="tv-pick-why"><b>Why this pick</b> {p.why}</p> : null;
}

/** One suggestion at a time; "Pick another" moves on and counts as a skip. */
export function SurpriseOverlay({ libId, filters, label, onClose }: { libId: string; filters: string; label: string; onClose: () => void }) {
  const toast = useToast();
  const nav = useNavigate();
  const s = useSurprise(libId, filters, toast);
  const p = s.cur;
  // Each new film starts on Play; the empty state on its only button.
  useEffect(() => {
    if (!s.loading) requestAnimationFrame(() => focusInitial(p ? "pk-play" : "pk-close"));
  }, [s.loading, p?.id]);
  // A like or dislike shapes later picks; the film stays on screen.
  const [votes, setVotes] = useState<Record<number, number>>({});
  const vote = (v: 1 | -1) => {
    if (!p) return;
    const now = votes[p.id] === v ? 0 : v;
    setVotes((x) => ({ ...x, [p.id]: now }));
    picks.feedback(now === 1 ? "like" : now === -1 ? "dislike" : "clear", p.id);
    toast(now === 1 ? `More like ${p.title}` : now === -1 ? `Less like ${p.title}` : "Cleared");
  };
  const act = (what: "watch" | "seen") => {
    if (!p) return;
    (what === "watch" ? pickWatchlist(p) : pickSeen(p)).then(
      () => toast(what === "watch" ? `Added to your Watchlist: ${p.title}` : `Marked as watched: ${p.title}`),
      (e: Error) => toast(e.message),
    );
    s.advance();
  };
  return (
    <Overlay className="tv-surprise" onClose={onClose}>
      {s.loading ? <Spinner /> : !p ? (
        <div className="tv-center">
          <h2 className="tv-h1">{s.error ? "Couldn't pick a film" : s.matching ? "That's everything for now" : "Nothing matches"}</h2>
          <p className="tv-muted">
            {s.error || (s.matching
              ? `${s.held ? `${s.held.toLocaleString()} ${s.held === 1 ? "film is" : "films are"} resting after being suggested, or hidden. ` : ""}Loosen a filter, or clear them in Filters → Suggestions.`
              : "No unwatched film fits these filters. Loosen one and try again.")}
          </p>
          <div className="tv-actions"><TvButton primary autoFocus fk="pk-close" onClick={onClose}>Back to the library</TvButton></div>
        </div>
      ) : (
        <div className="tv-pick" key={p.id}>
          {p.backdrop && <div className="tv-pick-bg" style={{ backgroundImage: `url(${art(p.backdrop, "w1280")})` }} />}
          <div className="tv-pick-shade" />
          <div className="tv-pick-body">
            <div className="tv-pick-poster"><Art src={art(p.poster, "w500")} title={p.title} /></div>
            <div className="tv-pick-info">
              <div className="tv-eyebrow">{label} · {s.left ? `${s.left} more lined up` : "last one"}</div>
              <h1 className="tv-detail-title">{p.title}</h1>
              <div className="tv-meta">
                {p.rating ? <span className="tv-rating">★ {p.rating.toFixed(1)}</span> : null}
                <span>{pickMeta(p)}</span>
                {p.moods.slice(0, 2).map((m) => <span key={m} className="tv-pick-mood">{m}</span>)}
              </div>
              {p.blurb && <p className="tv-overview">{p.blurb}</p>}
              <Why p={p} />
              <div className="tv-actions" data-group="pick-actions" data-remember="false">
                <TvButton primary autoFocus fk="pk-play" icon={<Play size={24} />}
                  onClick={() => playItem(p.id).catch((e: Error) => toast(e.message))}>Play</TvButton>
                <TvButton fk="pk-next" icon={<Dice size={22} />} onClick={s.another}>Pick another</TvButton>
                <TvButton fk="pk-details" icon={<Info size={22} />} onClick={() => (onClose(), nav(`/item/${p.id}`))}>Details</TvButton>
              </div>
              <div className="tv-actions tv-pick-minor" data-group="pick-minor" data-remember="false">
                <TvButton fk="pk-watch" className="chip" icon={<Plus size={20} />} onClick={() => act("watch")}>Watchlist</TvButton>
                <TvButton fk="pk-seen" className="chip" icon={<Check size={20} />} onClick={() => act("seen")}>Seen it</TvButton>
                <TvButton fk="pk-like" className={`chip ${votes[p.id] === 1 ? "on" : ""}`} icon={<ThumbUp size={20} />} onClick={() => vote(1)}>More like this</TvButton>
                <TvButton fk="pk-dislike" className={`chip ${votes[p.id] === -1 ? "on" : ""}`} icon={<ThumbDown size={20} />} onClick={() => vote(-1)}>Less like this</TvButton>
                <TvButton fk="pk-notint" className="chip" icon={<X size={20} />} onClick={s.notInterested}>Not interested</TvButton>
              </div>
            </div>
          </div>
        </div>
      )}
    </Overlay>
  );
}

/** Genres as three-way chips: off, never (✕), must have (+). Saves on every press. */
export function GenreChips({ libId, prefs, onChange, fkPrefix = "g" }: { libId: string; prefs: GenrePrefs; onChange: (g: GenrePrefs) => void; fkPrefix?: string }) {
  const [genres, setGenres] = useState<{ name: string }[]>(() => api.cached<{ name: string }[]>(`/api/libraries/${libId}/genres`) || []);
  useEffect(() => {
    api.get<{ name: string }[]>(`/api/libraries/${libId}/genres`).then(setGenres, () => {});
  }, [libId]);
  return (
    <div className="tv-genre-chips" data-group={`${fkPrefix}-chips`}>
      {genres.map((g) => {
        const m = genreMode(prefs, g.name);
        return (
          <TvButton key={g.name} fk={`${fkPrefix}-${g.name}`} className={`chip tv-gchip m${m}`} onClick={() => onChange(cycleGenre(prefs, g.name))}>
            {`${m === 1 ? "✕ " : m === 2 ? "+ " : ""}${g.name}`}
          </TvButton>
        );
      })}
    </div>
  );
}

/** The quiz, then three picks side by side. */
export function QuizOverlay({ libId, prefs, onPrefs, onClose }: { libId: string; prefs: GenrePrefs; onPrefs: (g: GenrePrefs) => void; onClose: () => void }) {
  const toast = useToast();
  const nav = useNavigate();
  const q = useQuiz(libId);
  const done = q.step >= QUIZ.length;
  const step = QUIZ[q.step];
  // Back steps back through the questions before it leaves.
  const back = useCallback(() => (q.step > 0 && !done ? q.back() : onClose()), [q.step, done, q.back, onClose]);
  const first = q.shown[0]?.id;
  useEffect(() => {
    if (done && q.loading) return;
    requestAnimationFrame(() => focusInitial(done ? (first ? `qp-play-${first}` : "qz-restart") : undefined));
  }, [q.step, done, q.loading, first]);

  const dismiss = (p: Pick, what: "watch" | "seen" | "notint") => {
    if (what === "notint") picks.feedback("notint", p.id);
    else (what === "watch" ? pickWatchlist(p) : pickSeen(p)).catch((e: Error) => toast(e.message));
    toast(what === "watch" ? `Added to your Watchlist: ${p.title}` : what === "seen" ? `Marked as watched: ${p.title}` : `${p.title} is hidden for a year`);
    q.dismiss(p);
  };

  return (
    <Overlay className="tv-quiz" onClose={back}>
      <div className="tv-quiz-body">
        <div className="tv-quiz-progress"><i style={{ width: `${(Math.min(q.step, QUIZ.length) / QUIZ.length) * 100}%` }} /></div>
        {!done && step ? (
          <>
            <div className="tv-eyebrow">Help me choose · question {q.step + 1} of {QUIZ.length}</div>
            <h1 className="tv-h1">{step.q}</h1>
            {step.note && <p className="tv-muted tv-quiz-note">{step.note}</p>}
            {step.k === "genres" ? (
              <>
                <GenreChips libId={libId} prefs={prefs} onChange={onPrefs} fkPrefix="qg" />
                <div className="tv-actions" data-group="quiz-nav"><TvButton primary autoFocus fk="qz-next" onClick={q.next}>Next</TvButton></div>
              </>
            ) : (
              <div className="tv-quiz-opts" data-group="quiz-opts" data-remember="false">
                {step.opts!.map((o, i) => (
                  <button key={o.v} className={`tv-quiz-opt ${q.answers[step.k as keyof typeof q.answers] === o.v ? "on" : ""}`} data-f data-fk={`qo-${o.v}`}
                    data-autofocus={i === 0 ? "" : undefined} onClick={() => q.answer(o.v)}>
                    <b>{o.l}</b>
                    {o.s && <span>{o.s}</span>}
                  </button>
                ))}
              </div>
            )}
          </>
        ) : q.loading ? <Spinner /> : (
          <>
            <div className="tv-eyebrow">Your picks</div>
            <h1 className="tv-h1">{q.shown.length ? "Here's what we reckon" : q.error ? "Couldn't pick" : q.found ? "That's every pick for these answers" : "Nothing new fits every answer"}</h1>
            {!q.shown.length && <p className="tv-muted tv-quiz-note">{q.error || (q.found ? "Shuffle for more, or start over." : "Try loosening one.")}</p>}
            <div className="tv-quiz-picks">
              {q.shown.map((p, i) => (
                <div key={p.id} className={`tv-quiz-pick ${i === 0 ? "first" : ""}`} data-group={`qp-${p.id}`} data-remember="false">
                  <div className="tv-quiz-pick-head">
                    <div className="tv-quiz-pick-poster"><Art src={art(p.poster, "w342")} title={p.title} /></div>
                    <div>
                      <div className="tv-eyebrow">{i === 0 ? "Top pick" : "Also great"}</div>
                      <h3>{p.title}</h3>
                      <div className="tv-muted">{p.rating ? `★ ${p.rating.toFixed(1)} · ` : ""}{pickMeta(p)}</div>
                    </div>
                  </div>
                  <Why p={p} />
                  <div className="tv-quiz-pick-actions">
                    <TvButton primary fk={`qp-play-${p.id}`} icon={<Play size={20} />} onClick={() => playItem(p.id).catch((e: Error) => toast(e.message))}>Play</TvButton>
                    <TvButton fk={`qp-info-${p.id}`} className="chip" onClick={() => (onClose(), nav(`/item/${p.id}`))}>Details</TvButton>
                  </div>
                  <div className="tv-quiz-pick-actions">
                    <TvButton fk={`qp-watch-${p.id}`} className="chip" onClick={() => dismiss(p, "watch")}>Watchlist</TvButton>
                    <TvButton fk={`qp-seen-${p.id}`} className="chip" onClick={() => dismiss(p, "seen")}>Seen it</TvButton>
                    <TvButton fk={`qp-notint-${p.id}`} className="chip" onClick={() => dismiss(p, "notint")}>Not interested</TvButton>
                  </div>
                </div>
              ))}
            </div>
            <div className="tv-actions" data-group="quiz-nav">
              <TvButton fk="qz-restart" icon={<Refresh size={22} />} onClick={q.restart}>Start over</TvButton>
              {q.found > 0 && <TvButton fk="qz-shuffle" icon={<Dice size={22} />} onClick={() => (q.shuffle(), focusKey("qz-shuffle"))}>Shuffle picks</TvButton>}
            </div>
          </>
        )}
      </div>
    </Overlay>
  );
}
