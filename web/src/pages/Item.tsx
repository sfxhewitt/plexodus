import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, art, Card, Detail, episodeLabel, fmtRuntime, fmtSize, fmtTime, resolution, ShareTarget, shareToPlex } from "../api";
import { Img, PosterCard, Row } from "../components/Cards";
import { MoodProfile } from "../components/Picks";
import { Check, Eye, Play, Refresh, Search, Share, Wand, X } from "../components/Icons";
import { useApi, useDebounced, useUser } from "../hooks";
import { initials } from "../people";
import { countLabel, episodeStatus, fmtAirDate, Guide, GuideEpisode, mergeEpisodes, mergeSeasons, seasonCard, seasonSub } from "../guide";
import { hasRT, RTScores } from "../rt";
import { forgetFinished, useFinishedTick, withFinished } from "../homeHidden";

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

function EpisodeList({ eps, guide }: { eps: Card[]; guide?: { list: ReturnType<typeof mergeEpisodes> } }) {
  const list = guide?.list ?? eps.map((c) => ({ episode: c.episodeNumber ?? 0, card: c, guide: undefined }));
  return (
    <div className="episodes">
      {list.map(({ episode, card: e, guide: g }) => !e ? g && <MissingEpisode key={`g${episode}`} g={g} /> : (
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

/** An episode TMDB lists that isn't in the library: greyed, with when it airs. */
function MissingEpisode({ g }: { g: GuideEpisode }) {
  return (
    <div className="episode missing">
      <div className="ep-thumb">
        <Img src={art(g.still, "w500")} alt={g.name || ""} title={`E${g.episode}`} />
      </div>
      <div className="ep-body">
        <div className="ep-title"><span className="ep-num">{g.episode}</span>{g.name || `Episode ${g.episode}`}</div>
        <div className="ep-meta"><span className={`ep-status ${g.state}`}>{episodeStatus(g)}</span>{g.runtime ? ` · ${fmtRuntime(g.runtime)}` : ""}</div>
        {g.overview && <p>{g.overview}</p>}
      </div>
    </div>
  );
}

/** Season tabs: the library's seasons and the ones you don't have. */
function SeasonTabs({ showId, current, siblings, guide }: { showId?: number; current?: number; siblings: Card[]; guide?: Guide }) {
  const tabs = mergeSeasons(siblings, guide);
  if (tabs.length < 2 || !showId) return null;
  return (
    <div className="season-tabs">
      {tabs.map(({ season, card, guide: g }) => (
        <Link key={season} to={card ? `/item/${card.id}` : `/item/${showId}/season/${season}`}
          className={`${season === current ? "on" : ""} ${card ? "" : "missing"}`}>
          {card?.title || g?.name || `Season ${season}`}
        </Link>
      ))}
    </div>
  );
}

/** A season that isn't in the library, from the episode guide. */
export function GuideSeasonPage() {
  const { id, n } = useParams();
  const season = Number(n);
  const { data: show, error } = useApi<Detail>(`/api/items/${id}`);
  const { data: guide } = useApi<Guide>(`/api/items/${id}/guide?season=${season}`);
  useEffect(() => {
    window.scrollTo(0, 0);
  }, [id, n]);
  if (error) return <div className="page-msg">{error}</div>;
  if (!show || !guide || guide.season !== season) return <div className="page-loading"><div className="spinner" /></div>;
  const g = guide.seasons.find((s) => s.season === season);
  const poster = g?.poster || show.poster;
  return (
    <div className="detail kind-season">
      <div className="detail-bg" style={show.backdrop ? { backgroundImage: `url(${art(show.backdrop, "w1280")})` } : undefined} />
      <div className="detail-shade" />
      <div className="detail-main page">
        <div className="detail-poster missing"><Img src={art(poster, "w500")} alt={g?.name || ""} title={g?.name || ""} /></div>
        <div className="detail-info">
          <Link className="crumb" to={`/item/${show.id}`}>{show.title}</Link>
          <h1>{g?.name || `Season ${season}`}</h1>
          <div className="meta-line">
            {g?.airDate ? <span>{g.airDate.slice(0, 4)}</span> : null}
            {guide.episodes.length ? <span>{guide.episodes.length} episodes</span> : null}
            <span className="pill">{g && g.aired > 0 ? "Not in your library" : g?.nextAirDate || g?.airDate ? `Coming ${fmtAirDate(g.nextAirDate || g.airDate)}` : "Coming soon"}</span>
          </div>
          {!guide.available && guide.message && <p className="overview">{guide.message}</p>}
        </div>
      </div>
      <div className="detail-below page">
        <section>
          <SeasonTabs showId={show.id} current={season} siblings={show.children} guide={guide} />
          <div className="episodes">{guide.episodes.map((e) => <MissingEpisode key={e.episode} g={e} />)}</div>
        </section>
      </div>
    </div>
  );
}

export default function Item() {
  const { id } = useParams();
  const nav = useNavigate();
  const { user } = useUser();
  const { data: raw, error, reload } = useApi<Detail>(`/api/items/${id}`);
  // Just back from the player: what it finished shows as watched at once.
  const finishedTick = useFinishedTick();
  const d = useMemo(() => raw && withFinished(raw), [raw, finishedTick]);
  const isTitle = d?.kind === "movie" || d?.kind === "show";
  const { data: extras } = useApi<{ cast: { id?: number; name: string; character?: string; profile?: string }[]; similar: Card[] }>(
    isTitle && String(d?.id) === id ? `/api/items/${id}/extras` : null);
  const [matching, setMatching] = useState(false);
  const { data: shareData } = useApi<{ targets: ShareTarget[] }>("/api/share/targets");
  const [shareMsg, setShareMsg] = useState<string>();
  const isSeriesPage = d?.kind === "show" || d?.kind === "season";
  const { data: guideData } = useApi<Guide>(isSeriesPage && String(d?.id) === id ? `/api/items/${id}/guide` : null);
  const guide = guideData?.available ? guideData : undefined;

  useEffect(() => {
    window.scrollTo(0, 0);
    setShareMsg(undefined);
  }, [id]);

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
    if (!watched) [d, ...d.children].forEach((c) => forgetFinished(c.id));
    api.invalidate("/api/home");
    reload();
  };
  const refresh = async () => {
    await api.post(`/api/items/${d.id}/refresh`);
    reload();
  };
  const allWatched = d.kind === "show" || d.kind === "season" ? !!d.leafCount && d.viewedLeafCount === d.leafCount : d.watched;
  const seasons = d.kind === "show" ? mergeSeasons(d.children, guide) : [];
  const seasonCount = seasons.filter((s) => s.season > 0).length || d.children.length;
  const thisSeason = d.kind === "season" ? guide?.seasons.find((s) => s.itemId === d.id) : undefined;
  const episodeCount = d.kind === "show" && guide ? countLabel(guide.have, guide.total)
    : thisSeason ? countLabel(thisSeason.have, thisSeason.total) : "";
  const next = d.kind === "show" ? guide?.next : guide?.next?.season === d.seasonNumber ? guide?.next : undefined;

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
            {hasRT(d) ? <RTScores c={d} labels /> : d.rating ? <span className="pill">★ {d.rating.toFixed(1)}</span> : null}
            {d.year ? <span>{d.year}</span> : null}
            {d.runtime ? <span>{fmtRuntime(d.runtime)}</span> : null}
            {d.kind === "show" && d.leafCount ? <span>{seasonCount} season{seasonCount === 1 ? "" : "s"} · {episodeCount || `${d.leafCount} episodes`}</span> : null}
            {d.kind === "season" && episodeCount ? <span>{episodeCount}</span> : null}
            {next?.airDate ? <span className="next-air">Next episode {fmtAirDate(next.airDate)}</span> : null}
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
            {(shareData?.targets ?? []).map((t) => (
              <button key={t.id} className="btn glass" title={`Add to ${t.name}'s Plex watchlist`} onClick={async () => {
                setShareMsg(`Sending to ${t.name}…`);
                try { setShareMsg(await shareToPlex(t, { itemId: d.id })); } catch (e) { setShareMsg((e as Error).message); }
              }}><Share size={18} /> Share with {t.name}</button>
            ))}
            {user?.isAdmin && (d.kind === "movie" || d.kind === "show") && (
              <>
                <button className="icon-btn glass" title="Fix match" onClick={() => setMatching(true)}><Wand /></button>
                <button className="icon-btn glass" title="Refresh metadata" onClick={refresh}><Refresh /></button>
              </>
            )}
            {d.imdbId && <a className="btn glass sm" href={`https://www.imdb.com/title/${d.imdbId}/`} target="_blank" rel="noreferrer">IMDb</a>}
          </div>
          {shareMsg && <div className="muted small" role="status" style={{ marginTop: 8 }}>{shareMsg}</div>}
          {d.metaState === "unmatched" && user?.isAdmin && (
            <div className="hint"><Search size={14} /> No metadata match found. Use Fix match to pick the right title.</div>
          )}
          <FileInfo d={d} />
        </div>
      </div>

      <div className="detail-below page">
        {d.kind === "movie" && <MoodProfile id={d.id} />}
        {d.kind === "show" && seasons.length > 0 && (
          <section>
            <h2 className="section-title">Seasons</h2>
            <div className="grid seasons">
              {seasons.map(({ season, card: s, guide: g }) => s ? (
                <PosterCard key={s.id} c={{ ...s, showPoster: d.poster }} subtitle={seasonSub(g, s)} />
              ) : g && (
                <PosterCard key={`g${season}`} c={seasonCard(g, d)} subtitle={seasonSub(g)} missing to={`/item/${d.id}/season/${season}`} />
              ))}
            </div>
          </section>
        )}
        {d.kind === "season" && (
          <section>
            <SeasonTabs showId={d.show?.id || d.showId || d.parentId} current={d.seasonNumber} siblings={d.siblings ?? []} guide={guide} />
            <EpisodeList eps={d.children} guide={guide ? { list: mergeEpisodes(d.children, guide, d.seasonNumber) } : undefined} />
          </section>
        )}
        {(d.kind === "movie" || d.kind === "show") && extras && extras.cast.length > 0 && (
          <section className="row row-cast">
            <div className="row-head"><h2 className="row-title">Cast</h2></div>
            <div className="row-track">
              {extras.cast.map((p) => {
                const body = (
                  <>
                    <div className="cast-photo">
                      {p.profile ? <img src={art(p.profile, "w185")} alt="" loading="lazy" /> : <span>{initials(p.name)}</span>}
                    </div>
                    <div className="t">{p.name}</div>
                    {p.character && <div className="s">{p.character}</div>}
                  </>
                );
                return p.id ? (
                  <Link key={p.name + p.character} to={`/person/${p.id}`} className="cast-card">{body}</Link>
                ) : (
                  <div key={p.name + p.character} className="cast-card">{body}</div>
                );
              })}
            </div>
          </section>
        )}
        {(d.kind === "movie" || d.kind === "show") && extras && extras.similar.length > 0 && (
          <Row title="More like this" kind="poster" items={extras.similar} />
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
