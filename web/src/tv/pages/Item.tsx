import { useCallback, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { api, art, Card, Detail, episodeLabel, fmtRuntime, fmtTime, resolution } from "../../api";
import { Check, Eye, Play, Refresh, Tv } from "../../components/Icons";
import { useApi } from "../../hooks";
import { Art, Spinner, TvButton, TvCard, TvRow, useBackdrop } from "../components";
import { usePageFocus, useRefresh, useToast } from "../hooks";
import { playItem } from "../play";

function MediaChips({ d }: { d: Detail }) {
  const f = d.files[0];
  if (!f) return null;
  const audio = f.streams?.filter((s) => s.type === "audio") || [];
  const subs = f.streams?.filter((s) => s.type === "subtitle") || [];
  const chips = [
    resolution(f),
    f.videoCodec?.toUpperCase(),
    f.audioCodec ? `${f.audioCodec.toUpperCase()}${audio[0]?.channels ? ` ${audio[0].channels > 2 ? `${audio[0].channels - 1}.1` : "2.0"}` : ""}` : "",
    audio.length > 1 ? `${audio.length} audio tracks` : "",
    subs.length ? `${subs.length} subtitle${subs.length === 1 ? "" : "s"}` : "",
    d.files.length > 1 ? `${d.files.length} versions` : "",
  ].filter(Boolean);
  if (!chips.length) return null;
  return <div className="tv-chips">{chips.map((c) => <span key={c} className="tv-chip">{c}</span>)}</div>;
}

/** First episode worth playing in a season: in progress, else first unwatched, else the first. */
function nextIn(eps: Card[]) {
  return eps.find((e) => e.position && !e.watched) || eps.find((e) => !e.watched) || eps[0];
}

export default function Item() {
  const { id } = useParams();
  const nav = useNavigate();
  const toast = useToast();
  const { data: d, error, reload } = useApi<Detail>(`/api/items/${id}`);
  useRefresh(reload);
  const [focusedEp, setFocusedEp] = useState<Card>();

  const ready = !!d && String(d.id) === id;
  const backdrop = d ? (d.kind !== "episode" && d.backdrop) || d.show?.backdrop || d.showBackdrop || d.backdrop : undefined;
  useBackdrop(art(backdrop, "w1280"));
  usePageFocus(ready);

  const play = useCallback((itemId: number, fromStart = false) => {
    playItem(itemId, { fromStart }).catch((e: Error) => toast(e.message));
  }, [toast]);

  if (error) return <div className="tv-center"><p className="tv-muted">{error}</p></div>;
  if (!d || !ready) return <Spinner />;

  const playable = d.kind === "movie" || d.kind === "episode";
  const target: Card | undefined = playable ? d : d.kind === "season" ? nextIn(d.children) : d.onDeck;
  const resume = target?.position && target.duration && !target.watched ? target.position : 0;
  const isSeries = d.kind === "show" || d.kind === "season";
  const allWatched = isSeries ? !!d.leafCount && d.viewedLeafCount === d.leafCount : d.watched;
  const poster = d.kind === "episode" ? undefined : d.poster || d.show?.poster || d.showPoster;
  const showTitle = d.show?.title || d.showTitle;
  const showId = d.show?.id || d.showId;

  const toggleWatched = async () => {
    try {
      await api.post(`/api/items/${d.id}/watched`, { watched: !allWatched });
      api.invalidate("/api/home");
      api.invalidate("/api/libraries/");
      reload();
    } catch (e) {
      toast((e as Error).message);
    }
  };

  const meta = [
    d.year ? String(d.year) : "",
    d.runtime ? fmtRuntime(d.runtime) : "",
    d.kind === "show" && d.children.length ? `${d.children.length} season${d.children.length === 1 ? "" : "s"}` : "",
    isSeries && d.leafCount ? `${d.leafCount} episodes` : "",
  ].filter(Boolean);

  return (
    <div className={`tv-page tv-detail kind-${d.kind}`} data-vscroll="reveal" data-margin="4rem">
      <section className="tv-detail-hero" data-row data-row-top>
        {poster ? (
          <Art src={art(poster, "w500")} title={d.title} className="tv-detail-poster" />
        ) : (
          <Art src={art(d.backdrop, "w780")} title={d.title} className="tv-detail-still" />
        )}
        <div className="tv-detail-info">
          {(d.kind === "season" || d.kind === "episode") && showTitle && <div className="tv-eyebrow">{showTitle}</div>}
          {d.logo && (d.kind === "movie" || d.kind === "show") ? (
            <img className="tv-detail-logo" src={art(d.logo, "w500")} alt={d.title} />
          ) : (
            <h1 className="tv-detail-title">
              {d.kind === "episode" && <span className="tv-ep-label">{episodeLabel(d)}</span>}
              {d.title}
            </h1>
          )}
          <div className="tv-meta">
            {d.rating ? <span className="tv-rating">★ {d.rating.toFixed(1)}</span> : null}
            {meta.map((m) => <span key={m}>{m}</span>)}
            {d.genres.slice(0, 3).map((g) => <span key={g} className="tv-genre">{g}</span>)}
            {allWatched && <span className="tv-watched"><Check size={18} /> Watched</span>}
          </div>
          {d.tagline && <p className="tv-tagline">{d.tagline}</p>}
          {d.overview && <p className="tv-overview">{d.overview}</p>}
          <div className="tv-actions" data-group="actions">
            {target && (
              <TvButton primary autoFocus fk="play" icon={<Play size={24} />} onClick={() => play(target.id)}>
                {resume ? `Resume from ${fmtTime(resume)}` : "Play"}
                {target !== d && target.kind === "episode" ? `  ·  ${episodeLabel(target)}` : ""}
              </TvButton>
            )}
            {resume > 0 && target && (
              <TvButton fk="restart" icon={<Refresh size={22} />} onClick={() => play(target.id, true)}>From the start</TvButton>
            )}
            <TvButton fk="watched" icon={allWatched ? <Eye size={22} /> : <Check size={22} />} onClick={toggleWatched}>
              {allWatched ? "Mark unwatched" : "Mark watched"}
            </TvButton>
            {(d.kind === "season" || d.kind === "episode") && showId && (
              <TvButton fk="show" icon={<Tv size={22} />} onClick={() => nav(`/item/${showId}`)}>Go to show</TvButton>
            )}
          </div>
          {playable && <MediaChips d={d} />}
        </div>
      </section>

      {d.kind === "show" && d.children.length > 0 && (
        <TvRow id={`seasons-${d.id}`} title="Seasons">
          {d.children.map((s) => (
            <TvCard key={s.id} c={{ ...s, showPoster: d.poster }} shape="poster" onSelect={() => nav(`/item/${s.id}`)}
              sub={`${s.leafCount || 0} episode${s.leafCount === 1 ? "" : "s"}${s.viewedLeafCount ? ` · ${s.viewedLeafCount} watched` : ""}`} />
          ))}
        </TvRow>
      )}

      {d.kind === "season" && (
        <>
          {d.siblings && d.siblings.length > 1 && (
            <div className="tv-tabs" data-row data-group="seasons">
              {d.siblings.map((s) => (
                <button key={s.id} data-f data-fk={`s${s.id}`} data-default={s.id === d.id ? "" : undefined}
                  className={`tv-tab ${s.id === d.id ? "on" : ""}`}
                  onClick={() => s.id !== d.id && nav(`/item/${s.id}`, { replace: true, state: { focus: `s${s.id}` } })}>
                  {s.title}
                </button>
              ))}
            </div>
          )}
          <TvRow id={`eps-${d.id}`} title="Episodes"
            extra={<EpisodeBlurb c={focusedEp && d.children.some((e) => e.id === focusedEp.id) ? focusedEp : d.children[0]} />}>
            {d.children.map((e) => (
              <TvCard key={e.id} c={{ ...e, showBackdrop: d.show?.backdrop || d.showBackdrop }} shape="land"
                label={`E${e.episodeNumber ?? ""}`} title={e.title} sub={e.runtime ? fmtRuntime(e.runtime) : ""}
                onFocusCard={setFocusedEp} onSelect={() => play(e.id)} onPlay={() => play(e.id)} />
            ))}
          </TvRow>
        </>
      )}

      {d.kind === "episode" && d.siblings && d.siblings.length > 1 && (
        <TvRow id={`sib-${d.parentId}`} title={d.parent?.title || "More episodes"}>
          {d.siblings.map((e) => (
            <TvCard key={e.id} c={{ ...e, showBackdrop: d.showBackdrop }} shape="land" label={`E${e.episodeNumber ?? ""}`}
              title={e.title} sub={e.id === d.id ? "Now viewing" : e.runtime ? fmtRuntime(e.runtime) : ""}
              onSelect={() => nav(`/item/${e.id}`, { replace: true })} onPlay={() => play(e.id)} />
          ))}
        </TvRow>
      )}
      <div className="tv-page-tail" />
    </div>
  );
}

function EpisodeBlurb({ c }: { c?: Card }) {
  if (!c) return null;
  return (
    <div className="tv-blurb">
      <div className="tv-blurb-title">
        <span className="tv-ep-label">{episodeLabel(c)}</span>{c.title}
        {c.watched && <span className="tv-watched"><Check size={16} /></span>}
      </div>
      {c.overview && <p>{c.overview}</p>}
    </div>
  );
}
