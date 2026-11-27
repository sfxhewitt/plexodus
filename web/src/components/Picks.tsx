// "Surprise me" and "Help me choose" on phones and the web: Reel Pick's picker
// and quiz as sheets over a film library. The flows live in ../picksHooks.
import { ReactNode, useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { art } from "../api";
import { cycleGenre, GenrePrefs, genreMode, ItemMood, Pick, pickMeta, picks, QUIZ } from "../picks";
import { pickSeen, pickWatchlist, parkPick, useQuiz, useSurprise } from "../picksHooks";
import { Img } from "./Cards";
import { Check, Dice, Play, Plus, Refresh, ThumbDown, ThumbUp, X } from "./Icons";
import "./picks.css";

/** A bottom sheet on phones, a centred dialog on wider screens. Esc and the backdrop close it. */
export function Sheet({ title, onClose, children, wide, foot }: { title: string; onClose: () => void; children: ReactNode; wide?: boolean; foot?: ReactNode }) {
  useEffect(() => {
    const key = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", key);
    document.body.classList.add("sheet-open");
    return () => {
      window.removeEventListener("keydown", key);
      document.body.classList.remove("sheet-open");
    };
  }, [onClose]);
  return (
    <div className="modal-back pk-back" onClick={onClose}>
      <div className={`modal pk-sheet${wide ? " wide" : ""}`} role="dialog" aria-modal="true" aria-label={title} onClick={(e) => e.stopPropagation()}>
        <div className="modal-head pk-head">
          <h2>{title}</h2>
          <button className="icon-btn" onClick={onClose} aria-label="Close"><X /></button>
        </div>
        <div className="pk-body">{children}</div>
        {foot && <div className="pk-foot">{foot}</div>}
      </div>
    </div>
  );
}

/** A short message that fades by itself. */
export function useSay(): [string, (m: string) => void] {
  const [msg, setMsg] = useState("");
  useEffect(() => {
    if (!msg) return;
    const t = setTimeout(() => setMsg(""), 2600);
    return () => clearTimeout(t);
  }, [msg]);
  return [msg, setMsg];
}

function Why({ p }: { p: Pick }) {
  return p.why ? <p className="pk-why"><b>Why this pick</b>{p.why}</p> : null;
}

export function SurpriseSheet({ libId, filters, label, resume, onClose }: { libId: string; filters: string; label: string; resume?: boolean; onClose: () => void }) {
  const nav = useNavigate();
  const [msg, say] = useSay();
  const s = useSurprise(libId, filters, say, resume);
  const p = s.cur;
  // Pick another is ready under Enter or Space for every film, to flick through them.
  const another = useRef<HTMLButtonElement>(null);
  useEffect(() => another.current?.focus({ preventScroll: true }), [p?.id]);
  const [votes, setVotes] = useState<Record<number, number>>({});
  const vote = (v: 1 | -1) => {
    if (!p) return;
    const now = votes[p.id] === v ? 0 : v;
    setVotes((x) => ({ ...x, [p.id]: now }));
    picks.feedback(now === 1 ? "like" : now === -1 ? "dislike" : "clear", p.id);
    say(now === 1 ? `More like ${p.title}` : now === -1 ? `Less like ${p.title}` : "Cleared");
  };
  const act = (what: "watch" | "seen") => {
    if (!p) return;
    (what === "watch" ? pickWatchlist(p) : pickSeen(p)).then(
      () => say(what === "watch" ? `Added to your Watchlist: ${p.title}` : `Marked as watched: ${p.title}`),
      (e: Error) => say(e.message),
    );
    s.advance();
  };
  return (
    <Sheet title={label} onClose={onClose}>
      {s.loading ? <div className="pk-empty"><div className="spinner" /></div> : !p ? (
        <div className="pk-empty">
          <h3>{s.error ? "Couldn't pick a film" : s.matching ? "That's everything for now" : "Nothing matches"}</h3>
          <p className="muted">
            {s.error || (s.matching
              ? `${s.held ? `${s.held.toLocaleString()} ${s.held === 1 ? "film is" : "films are"} resting after being suggested, or hidden. ` : ""}Loosen a filter, or bring them back under Filters.`
              : "No unwatched film fits these filters. Loosen one and try again.")}
          </p>
          <button className="btn primary" onClick={onClose}>Back to the library</button>
        </div>
      ) : (
        <div className="pk-pick" key={p.id}>
          {p.backdrop && <div className="pk-pick-bg" style={{ backgroundImage: `url(${art(p.backdrop, "w780")})` }} />}
          <div className="pk-pick-top">
            <Link to={`/item/${p.id}`} onClick={() => parkPick()} className="pk-poster"><Img src={art(p.poster, "w342")} alt={p.title} title={p.title} /></Link>
            <div className="pk-pick-info">
              <div className="pk-eyebrow">{s.left ? `${s.left} more lined up` : "Last one"}</div>
              <h3><Link to={`/item/${p.id}`} onClick={() => parkPick()}>{p.title}</Link></h3>
              <div className="muted pk-meta">{p.rating ? `★ ${p.rating.toFixed(1)} · ` : ""}{pickMeta(p)}</div>
              <div className="pk-tags">{p.moods.slice(0, 2).map((m) => <span key={m} className="pk-tag">{m}</span>)}</div>
            </div>
          </div>
          {p.blurb && <p className="pk-blurb">{p.blurb}</p>}
          <Why p={p} />
          <div className="pk-actions">
            <button ref={another} className="btn glass" onClick={s.another}><Dice size={18} /> Pick another</button>
            <button className="btn primary" onClick={() => (parkPick(), nav(`/play/${p.id}`))}><Play size={18} /> Play</button>
          </div>
          <div className="pk-actions minor">
            <button className="btn glass sm" onClick={() => act("watch")}><Plus size={16} /> Watchlist</button>
            <button className="btn glass sm" onClick={() => act("seen")}><Check size={16} /> Seen it</button>
            <button className={`btn glass sm ${votes[p.id] === 1 ? "on" : ""}`} onClick={() => vote(1)} aria-pressed={votes[p.id] === 1} title="More like this"><ThumbUp size={16} /></button>
            <button className={`btn glass sm ${votes[p.id] === -1 ? "on" : ""}`} onClick={() => vote(-1)} aria-pressed={votes[p.id] === -1} title="Less like this"><ThumbDown size={16} /></button>
            <button className="btn glass sm" onClick={s.notInterested} title="Hide for a year"><X size={16} /> Not interested</button>
          </div>
        </div>
      )}
      {msg && <div className="pk-toast" role="status">{msg}</div>}
    </Sheet>
  );
}

/** Genres as three-way chips: off, never (✕), must have (+). */
export function GenreChips({ genres, prefs, onChange }: { genres: string[]; prefs: GenrePrefs; onChange: (g: GenrePrefs) => void }) {
  return (
    <div className="pk-chips">
      {genres.map((g) => {
        const m = genreMode(prefs, g);
        return (
          <button key={g} className={`pk-chip g${m}`} aria-pressed={m > 0} aria-label={`${g}: ${["allowed", "never show", "must have"][m]}`}
            onClick={() => onChange(cycleGenre(prefs, g))}>{m === 1 ? "✕ " : m === 2 ? "+ " : ""}{g}</button>
        );
      })}
    </div>
  );
}

export function QuizSheet({ libId, genres, prefs, onPrefs, resume, onClose }: { libId: string; genres: string[]; prefs: GenrePrefs; onPrefs: (g: GenrePrefs) => void; resume?: boolean; onClose: () => void }) {
  const nav = useNavigate();
  const [msg, say] = useSay();
  const q = useQuiz(libId, resume);
  const done = q.step >= QUIZ.length;
  const step = QUIZ[q.step];
  const dismiss = useCallback((p: Pick, what: "watch" | "seen" | "notint") => {
    if (what === "notint") picks.feedback("notint", p.id);
    else (what === "watch" ? pickWatchlist(p) : pickSeen(p)).catch((e: Error) => say(e.message));
    say(what === "watch" ? `Added to your Watchlist: ${p.title}` : what === "seen" ? `Marked as watched: ${p.title}` : `${p.title} is hidden for a year`);
    q.dismiss(p);
  }, [q, say]);
  return (
    <Sheet title="Help me choose" onClose={onClose} wide={done}>
      <div className="pk-progress"><i style={{ width: `${(Math.min(q.step, QUIZ.length) / QUIZ.length) * 100}%` }} /></div>
      {!done && step ? (
        <div className="pk-quiz">
          <div className="pk-eyebrow">Question {q.step + 1} of {QUIZ.length}</div>
          <h3>{step.q}</h3>
          {step.note && <p className="muted">{step.note}</p>}
          {step.k === "genres" ? <GenreChips genres={genres} prefs={prefs} onChange={onPrefs} /> : (
            <div className="pk-opts">
              {step.opts!.map((o) => (
                <button key={o.v} className={`pk-opt ${q.answers[step.k as keyof typeof q.answers] === o.v ? "on" : ""}`} onClick={() => q.answer(o.v)}>
                  <b>{o.l}</b>
                  {o.s && <span>{o.s}</span>}
                </button>
              ))}
            </div>
          )}
          <div className="pk-nav">
            {q.step > 0 ? <button className="btn glass sm" onClick={q.back}>← Back</button> : <span />}
            {step.k === "genres" && <button className="btn primary sm" onClick={q.next}>Next →</button>}
          </div>
        </div>
      ) : q.loading ? <div className="pk-empty"><div className="spinner" /></div> : (
        <div className="pk-quiz">
          <div className="pk-eyebrow">Your picks</div>
          <h3>{q.shown.length ? "Here's what we reckon" : q.error ? "Couldn't pick" : q.found ? "That's every pick for these answers" : "Nothing new fits every answer"}</h3>
          {!q.shown.length && <p className="muted">{q.error || (q.found ? "Shuffle for more, or start over." : "Try loosening one.")}</p>}
          <div className="pk-results">
            {q.shown.map((p, i) => (
              <div key={p.id} className={`pk-result ${i === 0 ? "first" : ""}`}>
                <Link to={`/item/${p.id}`} onClick={() => parkPick()} className="pk-poster"><Img src={art(p.poster, "w342")} alt={p.title} title={p.title} /></Link>
                <div className="pk-result-info">
                  <div className="pk-eyebrow">{i === 0 ? "Top pick" : "Also great"}</div>
                  <h4><Link to={`/item/${p.id}`} onClick={() => parkPick()}>{p.title}</Link></h4>
                  <div className="muted pk-meta">{p.rating ? `★ ${p.rating.toFixed(1)} · ` : ""}{pickMeta(p)}</div>
                  {p.blurb && <p className="pk-blurb">{p.blurb}</p>}
                  <Why p={p} />
                  <div className="pk-actions minor">
                    <button className="btn primary sm" onClick={() => (parkPick(), nav(`/play/${p.id}`))}><Play size={16} /> Play</button>
                    <button className="btn glass sm" onClick={() => dismiss(p, "watch")}>Watchlist</button>
                    <button className="btn glass sm" onClick={() => dismiss(p, "seen")}>Seen it</button>
                    <button className="btn glass sm" onClick={() => dismiss(p, "notint")}>Not interested</button>
                  </div>
                </div>
              </div>
            ))}
          </div>
          <div className="pk-nav">
            <button className="btn glass sm" onClick={q.restart}><Refresh size={16} /> Start over</button>
            {q.found > 0 && <button className="btn primary sm" onClick={q.shuffle} title="Counts as a skip for these picks"><Dice size={16} /> Shuffle picks</button>}
          </div>
        </div>
      )}
      {msg && <div className="pk-toast" role="status">{msg}</div>}
    </Sheet>
  );
}

/** A film's mood profile on its page, with like and dislike. */
export function MoodProfile({ id }: { id: number }) {
  const [m, setM] = useState<ItemMood>();
  useEffect(() => {
    let alive = true;
    setM(undefined);
    picks.mood(id).then((x) => alive && setM(x), () => {});
    return () => {
      alive = false;
    };
  }, [id]);
  if (!m) return null;
  const vote = (v: 1 | -1) => {
    const now = m.vote === v ? 0 : v;
    setM({ ...m, vote: now });
    picks.feedback(now === 1 ? "like" : now === -1 ? "dislike" : "clear", id);
  };
  const hide = () => {
    setM({ ...m, hidden: !m.hidden });
    picks.feedback(m.hidden ? "unhide" : "notint", id);
  };
  return (
    <section className="pk-mood">
      <div className="pk-mood-head">
        <h2 className="section-title">Mood profile <small className="muted">{m.scored ? "scored" : "estimated from its genres"}</small></h2>
        <div className="pk-mood-votes">
          <button className={`btn glass sm ${m.vote === 1 ? "on" : ""}`} aria-pressed={m.vote === 1} onClick={() => vote(1)}><ThumbUp size={16} /> More like this</button>
          <button className={`btn glass sm ${m.vote === -1 ? "on" : ""}`} aria-pressed={m.vote === -1} onClick={() => vote(-1)}><ThumbDown size={16} /> Less like this</button>
          <button className={`btn glass sm ${m.hidden ? "on" : ""}`} aria-pressed={m.hidden} onClick={hide}
            title={m.hidden ? "Let it be suggested again" : "Never suggest it for a year"}><X size={16} /> {m.hidden ? "Hidden from picks" : "Not interested"}</button>
        </div>
      </div>
      <div className="pk-bars">
        {m.bars.map((b) => (
          <div key={b.key} className="pk-bar" title={`${b.label}: ${b.value}`}>
            <span><em>{b.low}</em><em>{b.high}</em></span>
            <i><b style={{ width: `${b.value}%` }} /></i>
          </div>
        ))}
      </div>
      <div className="pk-tags">
        {m.moods.map((x) => <span key={x} className="pk-tag">{x}</span>)}
        {m.notes.map((x) => <span key={x} className="pk-tag ok">{x}</span>)}
        {m.warnings.map((x) => <span key={x} className="pk-tag bad">{x}</span>)}
      </div>
    </section>
  );
}
