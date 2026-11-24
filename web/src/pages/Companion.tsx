import { FormEvent, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api, art, type NowPlaying } from "../api";
import { Sparkle } from "../components/Icons";

interface Note {
  at: number;
  kind: "character" | "context" | "language" | "detail" | "trivia";
  title: string;
  text: string;
}

interface CompanionData {
  enabled: boolean;
  stream: NowPlaying | null;
  notes: Note[];
  pending: boolean;
  grounded: boolean;
  notice?: string;
}

interface Answer {
  q: string;
  a?: string;
  err?: string;
  at: number;
}

const KIND: Record<Note["kind"], string> = {
  character: "Character",
  context: "Context",
  language: "Language",
  detail: "Easy to miss",
  trivia: "Trivia",
};

function clock(sec: number) {
  const t = Math.max(0, Math.floor(sec));
  const h = Math.floor(t / 3600), m = Math.floor((t / 60) % 60), s = t % 60;
  return h ? `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}` : `${m}:${String(s).padStart(2, "0")}`;
}

/**
 * Scene notes: a phone page that follows what this account is playing and
 * shows notes about the scene as playback reaches it (motivations, context,
 * dated language, trivia). Nothing ahead of the current moment is shown.
 */
export default function Companion() {
  const [data, setData] = useState<CompanionData | null>(null);
  const [error, setError] = useState("");
  const [tick, setTick] = useState(0);
  const got = useRef(0);
  const seen = useRef<Set<string>>(new Set());
  const first = useRef(true);
  const [fresh, setFresh] = useState<Set<string>>(new Set());
  const [answers, setAnswers] = useState<Answer[]>([]);
  const [q, setQ] = useState("");
  const [asking, setAsking] = useState(false);

  useEffect(() => {
    let alive = true;
    let timer = 0;
    const load = () => {
      api.get<CompanionData>("/api/companion").then((d) => {
        if (!alive) return;
        got.current = Date.now();
        setError("");
        setData(d);
        const keys = d.notes.map((n) => `${n.at}:${n.title}`);
        const added = keys.filter((k) => !seen.current.has(k));
        keys.forEach((k) => seen.current.add(k));
        if (!first.current && added.length) {
          setFresh(new Set(added));
          navigator.vibrate?.(30);
        }
        if (d.stream) first.current = false;
      }, (e: Error) => alive && setError(e.message)).finally(() => {
        if (alive) timer = window.setTimeout(load, document.hidden ? 15000 : 5000);
      });
    };
    load();
    const onVis = () => {
      if (!document.hidden) {
        clearTimeout(timer);
        load();
      }
    };
    document.addEventListener("visibilitychange", onVis);
    const t = window.setInterval(() => setTick((n) => n + 1), 1000);
    // Keep the phone awake while the page is open, where supported.
    let lock: { release: () => Promise<void> } | null = null;
    const nav = navigator as Navigator & { wakeLock?: { request: (t: "screen") => Promise<{ release: () => Promise<void> }> } };
    nav.wakeLock?.request("screen").then((l) => (lock = l), () => {});
    return () => {
      alive = false;
      clearTimeout(timer);
      clearInterval(t);
      document.removeEventListener("visibilitychange", onVis);
      lock?.release().catch(() => {});
    };
  }, []);

  const s = data?.stream;
  void tick;
  const pos = s ? s.position + (s.state === "playing" ? (Date.now() - got.current) / 1000 : 0) : 0;

  const ask = async (e: FormEvent) => {
    e.preventDefault();
    const question = q.trim();
    if (!question || asking) return;
    setAsking(true);
    setQ("");
    const entry: Answer = { q: question, at: pos };
    setAnswers((cur) => [entry, ...cur]);
    try {
      const r = await api.post<{ answer: string; at: number }>("/api/companion/ask", { question });
      entry.a = r.answer;
      entry.at = r.at;
    } catch (err) {
      entry.err = err instanceof Error ? err.message : String(err);
    }
    setAnswers((cur) => [...cur]);
    setAsking(false);
  };

  if (!data && error) return <div className="page-msg">{error}</div>;
  if (!data) return <div className="page-loading"><div className="spinner" /></div>;

  const title = s ? (s.kind === "episode" ? s.showTitle || s.title : s.title) : "";
  const sub = s
    ? s.kind === "episode"
      ? [s.seasonNumber != null && s.episodeNumber != null ? `S${s.seasonNumber} E${s.episodeNumber}` : "", s.title].filter(Boolean).join(" · ")
      : s.year ? String(s.year) : ""
    : "";
  const pct = s && s.duration ? Math.min(100, (pos / s.duration) * 100) : 0;
  const shown = data.notes;

  return (
    <div className="page companion-page">
      {s?.backdrop && <div className="cmp-bg" style={{ backgroundImage: `url(${art(s.backdrop, "w780")})` }} />}
      <div className="cmp-head">
        <h1><Sparkle size={20} /> Scene notes</h1>
        {s ? (
          <div className="cmp-now">
            {s.poster && <img src={art(s.poster, "w185")} alt="" />}
            <div className="cmp-now-text">
              <div className="t">{title}</div>
              {sub && <div className="s">{sub}</div>}
              <div className="cmp-time">
                <span className={`cmp-dot ${s.state}`} />
                {s.state === "playing" ? "Playing" : "Paused"} · {clock(pos)}{s.duration ? ` / ${clock(s.duration)}` : ""}
              </div>
              <div className="progress cmp-progress"><div style={{ width: `${pct}%` }} /></div>
            </div>
          </div>
        ) : (
          <p className="muted">Start something on your TV or another device and notes about each scene appear here as you watch. Keep this page open beside you.</p>
        )}
      </div>

      {!data.enabled && (
        <div className="cmp-card cmp-warn">
          Scene notes are written by Claude, so the server needs an Anthropic API key. Add one in{" "}
          <Link to="/settings">Settings → Plain-English search</Link> (admins), then come back here.
        </div>
      )}

      {s && data.enabled && (
        <form className="cmp-ask" onSubmit={ask}>
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Ask about this moment…" enterKeyHint="send" maxLength={500} />
          <button className="btn primary sm" disabled={!q.trim() || asking}>{asking ? "…" : "Ask"}</button>
        </form>
      )}

      {answers.map((a, i) => (
        <div key={`a${answers.length - i}`} className="cmp-card cmp-answer">
          <div className="cmp-kind">You asked at {clock(a.at)}</div>
          <div className="cmp-q">{a.q}</div>
          {a.a ? <p>{a.a}</p> : a.err ? <p className="err">{a.err}</p> : <p className="muted">Thinking…</p>}
        </div>
      ))}

      {s && data.enabled && data.pending && (
        <div className="cmp-card cmp-pending"><div className="spinner sm" /> Writing notes for this part{shown.length ? "" : ". The first ones take about a minute"}…</div>
      )}
      {s && data.notice && !data.pending && <div className="cmp-card cmp-warn">Couldn't write notes: {data.notice}</div>}
      {s && data.enabled && !data.pending && !data.notice && shown.length === 0 && (
        <div className="cmp-card muted">The first note for this part appears when the film reaches it.</div>
      )}

      <div className="cmp-notes">
        {shown.map((n) => {
          const k = `${n.at}:${n.title}`;
          return (
            <article key={k} className={`cmp-card cmp-note kind-${n.kind} ${fresh.has(k) ? "fresh" : ""}`}>
              <div className="cmp-kind">{KIND[n.kind] || n.kind} · {clock(n.at)}</div>
              <h3>{n.title}</h3>
              <p>{n.text}</p>
            </article>
          );
        })}
      </div>
      {s && shown.length > 0 && !data.grounded && (
        <p className="muted small cmp-foot">This copy has no readable subtitles yet, so note timings are approximate.</p>
      )}
    </div>
  );
}
