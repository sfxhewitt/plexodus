import { FormEvent, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api, art, type NowPlaying } from "../api";
import { Sparkle } from "../components/Icons";
import { canKeepAwake, KeepAwake } from "../keepAwake";

const AWAKE_KEY = "plexodus.scene.keepAwake";
function awakePref() {
  try {
    return localStorage.getItem(AWAKE_KEY) !== "0";
  } catch {
    return true;
  }
}
function savePref(on: boolean) {
  try {
    localStorage.setItem(AWAKE_KEY, on ? "1" : "0");
  } catch {
    // private mode: fine
  }
}

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
  const awake = useRef(new KeepAwake());
  const [awakeOn, setAwakeOn] = useState(false);
  const toggleAwake = async () => {
    if (awake.current.on) {
      awake.current.disable();
      setAwakeOn(false);
      savePref(false);
    } else {
      savePref(true);
      setAwakeOn(await awake.current.enable());
    }
  };

  useEffect(() => {
    let alive = true;
    // iPhones freeze a page in the background and can drop a request that
    // was in flight, so polling never waits on an old request: a fresh one
    // starts every few seconds, each with its own short time limit, and the
    // page also refreshes the moment it's shown again.
    let busySince = 0;
    const load = () => {
      if (!alive || (busySince && Date.now() - busySince < 12000)) return;
      const started = Date.now();
      busySince = started;
      const ctl = new AbortController();
      const limit = window.setTimeout(() => ctl.abort(), 10000);
      fetch("/api/companion", { credentials: "same-origin", cache: "no-store", signal: ctl.signal })
        .then(async (res) => {
          if (res.status === 401) window.dispatchEvent(new Event("plexodus:unauthorized"));
          if (!res.ok) throw new Error((await res.json().catch(() => ({})))?.error || res.statusText);
          return res.json() as Promise<CompanionData>;
        })
        .then((d) => {
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
        }, (e: Error) => alive && setError(e.name === "AbortError" ? "The server is slow to answer" : e.message))
        .finally(() => {
          clearTimeout(limit);
          if (busySince === started) busySince = 0;
        });
    };
    load();
    const poll = window.setInterval(() => !document.hidden && load(), 4000);
    const wake = () => {
      if (document.hidden) return;
      busySince = 0;
      load();
    };
    document.addEventListener("visibilitychange", wake);
    window.addEventListener("focus", wake);
    window.addEventListener("pageshow", wake);
    window.addEventListener("online", wake);
    const t = window.setInterval(() => setTick((n) => n + 1), 1000);
    // Keep the phone awake: needs a tap, so the first touch anywhere turns
    // it on unless the viewer switched it off.
    const firstTap = (e: PointerEvent) => {
      if ((e.target as Element | null)?.closest?.(".cmp-awake")) return; // the button does it
      if (awakePref()) awake.current.enable().then(setAwakeOn, () => {});
    };
    document.addEventListener("pointerdown", firstTap, { once: true, capture: true });
    const reAwake = () => awake.current.resume();
    document.addEventListener("visibilitychange", reAwake);
    return () => {
      alive = false;
      clearInterval(poll);
      clearInterval(t);
      document.removeEventListener("visibilitychange", wake);
      document.removeEventListener("visibilitychange", reAwake);
      document.removeEventListener("pointerdown", firstTap, { capture: true });
      window.removeEventListener("focus", wake);
      window.removeEventListener("pageshow", wake);
      window.removeEventListener("online", wake);
      awake.current.disable();
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
        <div className="cmp-title">
          <h1><Sparkle size={20} /> Scene notes</h1>
          {canKeepAwake() && (
            <button className={`btn sm ${awakeOn ? "primary" : "glass"} cmp-awake`} onClick={(e) => { e.stopPropagation(); toggleAwake(); }}
              aria-pressed={awakeOn} title="Stop the phone's screen from turning off">
              {awakeOn ? "Screen stays on" : "Keep screen on"}
            </button>
          )}
        </div>
        {!canKeepAwake() && (
          // Phones only let a page keep the screen on over https.
          <p className="muted small cmp-awake-tip">To stop your phone locking mid-film, turn Auto-Lock off while you watch (iPhone: Settings → Display &amp; Brightness → Auto-Lock → Never).</p>
        )}
        {s ? (
          <div className="cmp-now">
            {s.poster && <img src={art(s.poster, "w185")} alt="" />}
            <div className="cmp-now-text">
              <div className="t">{title}</div>
              {sub && <div className="s">{sub}</div>}
              <div className="cmp-time">
                <span className={`cmp-dot ${s.state}`} />
                {s.state === "playing" ? "Playing" : "Paused"} · {clock(pos)}{s.duration ? ` / ${clock(s.duration)}` : ""}
                {Date.now() - got.current > 20000 && <span className="cmp-stale"> · reconnecting…</span>}
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
