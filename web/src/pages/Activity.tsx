import { useEffect } from "react";
import { Link } from "react-router-dom";
import { Activity as ActivityData, art, fmtTime, NowPlaying, RecentPlay } from "../api";
import { Pause, Play } from "../components/Icons";
import { useApi } from "../hooks";

const POLL_MS = 5000;

function episodeLabel(p: { kind: string; title: string; showTitle?: string; seasonNumber?: number; episodeNumber?: number }) {
  if (p.kind !== "episode" || !p.showTitle) return { main: p.title, sub: "" };
  const se = p.seasonNumber != null && p.episodeNumber != null ? `S${p.seasonNumber} · E${p.episodeNumber} · ` : "";
  return { main: p.showTitle, sub: se + p.title };
}

function quality(s: NowPlaying) {
  const h = s.height || 0;
  const res = h >= 2000 ? "4K" : h >= 1000 ? "1080p" : h >= 700 ? "720p" : h ? `${h}p` : "";
  return [res, s.videoCodec?.toUpperCase(), s.audioCodec?.toUpperCase(), s.container?.toUpperCase()].filter(Boolean).join(" · ");
}

function mbps(bits?: number) {
  return bits ? `${(bits / 1e6).toFixed(bits > 1e7 ? 0 : 1)} Mbps` : "";
}

function ago(ts: number) {
  const s = Math.max(0, Date.now() / 1000 - ts);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return new Date(ts * 1000).toLocaleDateString();
}

function StreamCard({ s }: { s: NowPlaying }) {
  const { main, sub } = episodeLabel(s);
  const pct = s.duration ? Math.min(100, (s.position / s.duration) * 100) : 0;
  const who = s.profile ? `${s.username} · ${s.profile}` : s.username;
  return (
    <div className="np-card">
      {s.backdrop && <div className="np-bg" style={{ backgroundImage: `url(${art(s.backdrop, "w780")})` }} />}
      <Link to={`/item/${s.itemId}`} className="np-poster">
        {s.poster ? <img src={art(s.poster, "w185")} alt="" loading="lazy" /> : <span />}
      </Link>
      <div className="np-body">
        <div className="np-who">
          <span className={`np-state ${s.state}`}>{s.state === "playing" ? <Play size={14} /> : <Pause size={14} />} {s.state === "playing" ? "Playing" : "Paused"}</span>
          <b>{who}</b>
        </div>
        <Link to={`/item/${s.itemId}`} className="np-title">{main}</Link>
        {sub && <div className="np-sub">{sub}</div>}
        <div className="np-facts">
          <span>{s.device} · {s.app}</span>
          <span className={`chip ${s.method === "direct" ? "hi" : ""}`}>{s.method === "direct" ? "Direct play" : "Remux"}</span>
          <span>{quality(s)}{s.bitrate ? ` · ${mbps(s.bitrate)}` : ""}</span>
          <span className="muted">{s.local ? "Local" : "Remote"} · {s.ip}</span>
        </div>
        <div className="np-progress">
          <div className="np-bar"><div style={{ width: `${pct}%` }} /></div>
          <span className="muted small">{fmtTime(s.position)} / {fmtTime(s.duration)}</span>
        </div>
      </div>
    </div>
  );
}

function RecentRow({ p }: { p: RecentPlay }) {
  const { main, sub } = episodeLabel(p);
  return (
    <Link to={`/item/${p.itemId}`} className="np-recent">
      {p.poster ? <img src={art(p.poster, "w185")} alt="" loading="lazy" /> : <span className="np-recent-ph" />}
      <div>
        <div className="t">{main}{sub && <span className="muted"> · {sub}</span>}</div>
        <div className="muted small">
          {p.profile ? `${p.username} · ${p.profile}` : p.username} · {ago(p.at)}{p.source === "plex" ? " · in Plex" : ""}
        </div>
      </div>
    </Link>
  );
}

export default function Activity() {
  const { data, error, reload } = useApi<ActivityData>("/api/activity");
  useEffect(() => {
    const t = setInterval(() => document.visibilityState === "visible" && reload(), POLL_MS);
    return () => clearInterval(t);
  }, [reload]);
  const streams = data?.streams || [];
  return (
    <div className="settings page np-page">
      <h1>Now watching</h1>
      {error && <div className="err">{error}</div>}
      <section className="settings-section">
        <h2>{streams.length ? `${streams.length} ${streams.length === 1 ? "stream" : "streams"}` : "Nobody is watching right now"}</h2>
        <div className="np-list">
          {streams.map((s) => <StreamCard key={s.key} s={s} />)}
        </div>
      </section>
      {!!data?.recent?.length && (
        <section className="settings-section">
          <h2>Recently watched</h2>
          <div className="card np-recent-list">
            {data.recent.map((p, i) => <RecentRow key={`${p.itemId}-${p.at}-${i}`} p={p} />)}
          </div>
        </section>
      )}
    </div>
  );
}
