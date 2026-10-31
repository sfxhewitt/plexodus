import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, art, Card, Detail, episodeLabel, fmtRuntime, fmtSize, fmtTime, resolution } from "../api";
import { Img, PosterCard, Row } from "../components/Cards";
import { Check, Eye, Play, Refresh, Search, Wand, X } from "../components/Icons";
import { useApi, useDebounced, useUser } from "../hooks";

function MatchModal({ item, onClose, onDone }: { item: Detail; onClose: () => void; onDone: () => void }) {
  const [q, setQ] = useState(item.title);
  const [year, setYear] = useState(item.year ? String(item.year) : "");
  const dq = useDebounced(q, 350);
  const dy = useDebounced(year, 350);
  const { data, error } = useApi<{ tmdbId: number; title: string; year: string; poster: string; overview: string }[]>(
    `/api/items/${item.id}/match?q=${encodeURIComponent(dq)}&year=${dy}`,
  );
  const [busy, setBusy] = useState(0);
  const pick = async (tmdbId: number) => {
    setBusy(tmdbId);
    try {
      await api.post(`/api/items/${item.id}/match`, { tmdbId });
      onDone();
    } finally {
      setBusy(0);
    }
  };
  return (
    <div className="modal-back" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <h2>Fix match</h2>
          <button className="icon-btn" onClick={onClose} aria-label="Close"><X /></button>
        </div>
        <div className="match-inputs">
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Title" autoFocus />
          <input value={year} onChange={(e) => setYear(e.target.value.replace(/\D/g, "").slice(0, 4))} placeholder="Year" className="year" />
        </div>
        {error && <div className="err">{error}</div>}
        <div className="match-list">
          {data?.map((c) => (
            <button key={c.tmdbId} className="match-row" onClick={() => pick(c.tmdbId)} disabled={!!busy}>
              <Img src={art(c.poster, "w185")} alt={c.title} title={c.title} className="match-poster" />
              <div>
                <div className="t">{c.title} {c.year && <span className="muted">({c.year})</span>}</div>
                <div className="s">{c.overview}</div>
              </div>
              {busy === c.tmdbId && <div className="spinner sm" />}
            </button>
          ))}
          {data && data.length === 0 && <div className="muted">No results.</div>}
        </div>
      </div>
    </div>
  );
}

function FileInfo({ d }: { d: Detail }) {
  const { user } = useUser();
  if (!d.files.length) return null;
  return (
    <div className="files">
      {d.files.map((f) => {
        const audio = f.streams?.filter((s) => s.type === "audio") || [];
        const subs = f.streams?.filter((s) => s.type === "subtitle") || [];
        return (
          <div key={f.id} className="file">
            <div className="chips">
              {resolution(f) && <span className="chip hi">{resolution(f)}</span>}
              {f.videoCodec && <span className="chip">{f.videoCodec.toUpperCase()}</span>}
              {f.audioCodec && <span className="chip">{f.audioCodec.toUpperCase()}</span>}
              {f.duration ? <span className="chip">{fmtTime(f.duration)}</span> : null}
              <span className="chip">{fmtSize(f.size)}</span>
              {f.bitrate ? <span className="chip">{(f.bitrate / 1e6).toFixed(1)} Mbps</span> : null}
              {!f.probed && <span className="chip dim">not analysed yet</span>}
            </div>
            {(audio.length > 0 || subs.length > 0) && (
              <div className="streams muted">
                {audio.length > 0 && <span>Audio: {audio.map((a) => `${a.language || "und"} ${a.codec}${a.channels ? ` ${a.channels}ch` : ""}`).join(", ")}</span>}
                {subs.length > 0 && <span>Subtitles: {subs.map((s) => s.language || "und").join(", ")}</span>}
              </div>
            )}
            {user?.isAdmin && f.path && <div className="path">{f.path}</div>}
          </div>
        );
      })}
    </div>
  );
}

function EpisodeList({ eps }: { eps: Card[] }) {
  return (
    <div className="episodes">
      {eps.map((e) => (
        <div key={e.id} className="episode">
          <Link to={`/play/${e.id}`} className="ep-thumb">
            <Img src={art(e.backdrop, "w500")} alt={e.title} title={`E${e.episodeNumber}`} />
            {e.position && e.duration ? <div className="progress"><div style={{ width: `${(e.position / e.duration) * 100}%` }} /></div> : null}
            <span className="ep-play"><Play size={22} /></span>
            {e.watched && <span className="badge done"><Check size={12} /></span>}
          </Link>
          <Link to={`/item/${e.id}`} className="ep-body">
            <div className="ep-title"><span className="ep-num">{e.episodeNumber}</span>{e.title}</div>
            <div className="ep-meta muted">{e.runtime ? fmtRuntime(e.runtime) : ""}</div>
            {e.overview && <p>{e.overview}</p>}
          </Link>
        </div>
      ))}
    </div>
  );
}

export default function Item() {
  const { id } = useParams();
  const nav = useNavigate();
  const { user } = useUser();
  const { data: d, error, reload } = useApi<Detail>(`/api/items/${id}`);
  const [matching, setMatching] = useState(false);

  useEffect(() => window.scrollTo(0, 0), [id]);

  if (error) return <div className="page-msg">{error}</div>;
  if (!d) return <div className="page-loading"><div className="spinner" /></div>;

  const backdrop = d.backdrop && d.kind !== "episode" ? d.backdrop : d.show?.backdrop || d.showBackdrop || d.backdrop;
  const poster = d.poster || d.show?.poster || d.showPoster;
  const playable = d.kind === "movie" || d.kind === "episode";
  const target = playable ? d : d.onDeck;
  const resume = target?.position && target.duration ? target.position : 0;

  const toggleWatched = async () => {
    const watched = d.kind === "show" || d.kind === "season" ? d.viewedLeafCount !== d.leafCount : !d.watched;
    await api.post(`/api/items/${d.id}/watched`, { watched });
    api.invalidate("/api/home");
    reload();
  };
  const refresh = async () => {
    await api.post(`/api/items/${d.id}/refresh`);
    reload();
  };
  const allWatched = d.kind === "show" || d.kind === "season" ? !!d.leafCount && d.viewedLeafCount === d.leafCount : d.watched;

  return (
    <div className={`detail kind-${d.kind}`}>
      <div className="detail-bg" style={backdrop ? { backgroundImage: `url(${art(backdrop, "w1280")})` } : undefined} />
      <div className="detail-shade" />
      <div className="detail-main page">
        <div className="detail-poster">
          {d.kind === "episode" ? (
            <Img src={art(d.backdrop, "w780")} alt={d.title} title={d.title} className="still" />
          ) : (
            <Img src={art(poster, "w500")} alt={d.title} title={d.title} />
          )}
        </div>
        <div className="detail-info">
          {(d.show || d.kind === "episode") && (
            <Link className="crumb" to={`/item/${d.show?.id || d.showId}`}>{d.show?.title || d.showTitle}</Link>
          )}
          {d.logo && d.kind !== "season" ? (
            <img className="detail-logo" src={art(d.logo, "w500")} alt={d.title} />
          ) : (
            <h1>{d.kind === "episode" && <span className="ep-label">{episodeLabel(d)}</span>}{d.title}</h1>
          )}
          <div className="meta-line">
            {d.rating ? <span className="pill">★ {d.rating.toFixed(1)}</span> : null}
            {d.year ? <span>{d.year}</span> : null}
            {d.runtime ? <span>{fmtRuntime(d.runtime)}</span> : null}
            {d.kind === "show" && d.leafCount ? <span>{d.children.length} seasons · {d.leafCount} episodes</span> : null}
            {d.genres.slice(0, 4).map((g) => <span key={g} className="genre">{g}</span>)}
          </div>
          {d.tagline && <p className="tagline">{d.tagline}</p>}
          {d.overview && <p className="overview">{d.overview}</p>}

          <div className="actions">
            {target && (
              <button className="btn primary" onClick={() => nav(`/play/${target.id}`)}>
                <Play size={18} />
                {resume ? `Resume ${fmtTime(resume)}` : "Play"}
                {target !== d && target.kind === "episode" && <span className="btn-sub">{episodeLabel(target)}</span>}
              </button>
            )}
            {resume > 0 && playable && (
              <button className="btn glass" onClick={() => nav(`/play/${d.id}?from=0`)}>From start</button>
            )}
            <button className="btn glass" onClick={toggleWatched}>
              {allWatched ? <><Eye size={18} /> Mark unwatched</> : <><Check size={18} /> Mark watched</>}
            </button>
            {user?.isAdmin && (d.kind === "movie" || d.kind === "show") && (
              <>
                <button className="icon-btn glass" title="Fix match" onClick={() => setMatching(true)}><Wand /></button>
                <button className="icon-btn glass" title="Refresh metadata" onClick={refresh}><Refresh /></button>
              </>
            )}
            {d.imdbId && <a className="btn glass sm" href={`https://www.imdb.com/title/${d.imdbId}/`} target="_blank" rel="noreferrer">IMDb</a>}
          </div>
          {d.metaState === "unmatched" && user?.isAdmin && (
            <div className="hint"><Search size={14} /> No metadata match found. Use Fix match to pick the right title.</div>
          )}
          <FileInfo d={d} />
        </div>
      </div>

      <div className="detail-below page">
        {d.kind === "show" && d.children.length > 0 && (
          <section>
            <h2 className="section-title">Seasons</h2>
            <div className="grid seasons">
              {d.children.map((s) => (
                <PosterCard key={s.id} c={{ ...s, showPoster: d.poster }}
                  subtitle={`${s.leafCount} episode${s.leafCount === 1 ? "" : "s"}${s.viewedLeafCount ? ` · ${s.viewedLeafCount} watched` : ""}`} />
              ))}
            </div>
          </section>
        )}
        {d.kind === "season" && (
          <section>
            {d.siblings && d.siblings.length > 1 && (
              <div className="season-tabs">
                {d.siblings.map((s) => (
                  <Link key={s.id} to={`/item/${s.id}`} className={s.id === d.id ? "on" : ""}>{s.title}</Link>
                ))}
              </div>
            )}
            <EpisodeList eps={d.children} />
          </section>
        )}
        {d.kind === "episode" && d.siblings && d.siblings.length > 1 && (
          <Row title={d.parent?.title || "More episodes"} link={`/item/${d.parentId}`} kind="landscape"
            items={d.siblings.map((s) => ({ ...s, showTitle: d.showTitle, backdrop: s.backdrop || d.showBackdrop }))} />
        )}
      </div>
      {matching && <MatchModal item={d} onClose={() => setMatching(false)} onDone={() => { setMatching(false); api.invalidate(); reload(); }} />}
    </div>
  );
}
