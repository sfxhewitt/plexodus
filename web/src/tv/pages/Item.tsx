import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { api, art, Card, Detail, episodeLabel, fmtRuntime, fmtTime, resolution, ShareTarget, shareToPlex } from "../../api";
import { Check, Eye, Play, Plus, Refresh, Share, Tv, X } from "../../components/Icons";
import { useApi } from "../../hooks";
import { Spinner, TvButton, TvCard, TvRow, useBackdrop } from "../components";
import { usePageFocus, useRefresh, useToast } from "../hooks";
import { openTrailerFull, requestTrailer, stopTrailer, trailersSupported } from "../ambient";
import { onUpFromTop } from "../focus";
import { hasTrailerKey as hasTrailerKeyNow, trailerKey } from "../Trailer";
import { playCard, playItem } from "../play";
import { CollectionTile } from "../collectionTile";
import { initials } from "../../people";
import { countLabel, episodeCard, episodeStatus, fmtAirDate, Guide, GuideEpisode, mergeEpisodes, mergeSeasons, seasonCard, seasonSub } from "../../guide";
import { hasRT, RTScores } from "../../rt";
import { forgetFinished, useFinishedTick, withFinished } from "../../homeHidden";

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
  const { data: raw, error, reload } = useApi<Detail>(`/api/items/${id}`);
  // Back from the player: what it just finished shows as watched straight
  // away, whatever the server's copy still says.
  const finishedTick = useFinishedTick();
  const d = useMemo(() => raw && withFinished(raw), [raw, finishedTick]);
  const { data: shareData } = useApi<{ targets: ShareTarget[] }>("/api/share/targets");
  const shareTargets = shareData?.targets ?? [];
  const isTitle = d?.kind === "movie" || d?.kind === "show";
  const { data: extras } = useApi<Extras>(isTitle && String(d?.id) === id ? `/api/items/${id}/extras` : null);
  useRefresh(reload);
  const [focusedEp, setFocusedEp] = useState<Card>();
  // Every episode TMDB lists, aired or not; loads after the page so it never slows it.
  const isSeriesPage = d?.kind === "show" || d?.kind === "season";
  const { data: guideData } = useApi<Guide>(isSeriesPage && String(d?.id) === id ? `/api/items/${id}/guide` : null);
  const guide = guideData?.available ? guideData : undefined;

  const ready = !!d && String(d.id) === id;
  const backdrop = d ? (d.kind !== "episode" && d.backdrop) || d.show?.backdrop || d.showBackdrop || d.backdrop : undefined;
  useBackdrop(art(backdrop, "w1280"));
  usePageFocus(ready);
  // A film's or show's trailer fades in behind the page once you've settled on it.
  const trailerId = ready && (d.kind === "movie" || d.kind === "show") ? d.id : undefined;
  useEffect(() => {
    if (trailerId === undefined) return;
    requestTrailer(trailerId, 2500);
    return stopTrailer;
  }, [trailerId]);
  // Up from the top of the page shows the trailer full screen, with sound
  // (instead of going to the tab bar; Back still leaves the page).
  const [hasTrailer, setHasTrailer] = useState(false);
  useEffect(() => {
    setHasTrailer(false);
    if (trailerId === undefined || !trailersSupported()) return;
    let live = true;
    trailerKey(trailerId).then((k) => live && setHasTrailer(!!k), () => {});
    const off = onUpFromTop(() => hasTrailerKeyNow(trailerId) && openTrailerFull(trailerId));
    return () => {
      live = false;
      off();
    };
  }, [trailerId]);

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
  const showTitle = d.show?.title || d.showTitle;
  const showId = d.show?.id || d.showId;

  const toggleWatched = async () => {
    try {
      await api.post(`/api/items/${d.id}/watched`, { watched: !allWatched });
      if (allWatched) [d, ...d.children].forEach((c) => forgetFinished(c.id));
      api.invalidate("/api/home");
      api.invalidate("/api/libraries/");
      reload();
    } catch (e) {
      toast((e as Error).message);
    }
  };

  const share = async (t: ShareTarget) => {
    toast(`Sending to ${t.name}…`);
    try {
      toast(await shareToPlex(t, { itemId: d.id }));
    } catch (e) {
      toast((e as Error).message);
    }
  };

  const toggleWatchlist = async () => {
    try {
      await api.post(`/api/items/${d.id}/watchlist`, { on: !d.watchlisted });
      api.invalidate("/api/home");
      toast(d.watchlisted ? "Removed from your Watchlist" : "Added to your Watchlist");
      reload();
    } catch (e) {
      toast((e as Error).message);
    }
  };

  const seasons = d.kind === "show" ? mergeSeasons(d.children, guide) : [];
  const thisSeason = d.kind === "season" ? guide?.seasons.find((s) => s.itemId === d.id) : undefined;
  const episodes = d.kind === "season" ? mergeEpisodes(d.children, guide, d.seasonNumber) : [];
  const seasonCount = seasons.filter((s) => s.season > 0).length || d.children.length;
  const episodeCount = d.kind === "show" && guide ? countLabel(guide.have, guide.total)
    : thisSeason ? countLabel(thisSeason.have, thisSeason.total) : "";
  const meta = [
    d.year ? String(d.year) : "",
    d.runtime ? fmtRuntime(d.runtime) : "",
    d.kind === "show" && seasonCount ? `${seasonCount} season${seasonCount === 1 ? "" : "s"}` : "",
    episodeCount || (isSeries && d.leafCount ? `${d.leafCount} episodes` : ""),
  ].filter(Boolean);
  const next = d.kind === "show" ? guide?.next : guide?.next?.season === d.seasonNumber ? guide?.next : undefined;
  const nextLabel = next?.airDate ? `Next episode ${fmtAirDate(next.airDate)}` : "";
  const focusedEntry = episodes.find((e) => (e.card ?? (e.guide && episodeCard(e.guide, d)))?.id === focusedEp?.id);

  return (
    <div className={`tv-page tv-detail kind-${d.kind}`} data-vscroll="reveal" data-margin="4rem">
      <section className="tv-detail-hero" data-row data-row-top>
        <div className="tv-detail-info">
          {hasTrailer && <div className="tv-trailer-hint"><span>▲</span> Trailer full screen</div>}
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
            {hasRT(d) ? <RTScores c={d} labels className="tv-rt" /> : d.rating ? <span className="tv-rating">★ {d.rating.toFixed(1)}</span> : null}
            {meta.map((m) => <span key={m}>{m}</span>)}
            {nextLabel && <span className="tv-next-air">{nextLabel}</span>}
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
            {(d.kind === "movie" || d.kind === "show") && (
              <TvButton fk="watchlist" icon={d.watchlisted ? <X size={22} /> : <Plus size={22} />} onClick={toggleWatchlist}>
                {d.watchlisted ? "On your Watchlist" : "Watchlist"}
              </TvButton>
            )}
            {shareTargets.map((t) => (
              <TvButton key={t.id} fk={`share-${t.id}`} icon={<Share size={22} />} onClick={() => share(t)}>
                Share with {t.name}
              </TvButton>
            ))}
            {(d.kind === "season" || d.kind === "episode") && showId && (
              <TvButton fk="show" icon={<Tv size={22} />} onClick={() => nav(`/item/${showId}`)}>Go to show</TvButton>
            )}
          </div>
          {playable && <MediaChips d={d} />}
        </div>
      </section>

      {d.kind === "movie" && d.collection && d.collectionItems && d.collectionItems.length > 1 && (
        <TvRow id={`col-${d.collection.id}`} title={d.collection.name}>
          {d.collectionItems.map((f) => (
            <TvCard key={f.id} c={f} shape="poster" isDefault={f.id === d.id} sub={f.id === d.id ? "Now viewing" : undefined}
              onSelect={() => f.id !== d.id && nav(`/item/${f.id}`)} onPlay={() => play(f.id)} />
          ))}
          <CollectionTile c={d.collection} onSelect={(c) => nav(`/collection/${c.id}`)} />
        </TvRow>
      )}

      {d.kind === "show" && seasons.length > 0 && (
        <TvRow id={`seasons-${d.id}`} title="Seasons">
          {seasons.map(({ season, card: s, guide: g }) => s ? (
            <TvCard key={s.id} c={{ ...s, showPoster: d.poster }} shape="poster" onSelect={() => nav(`/item/${s.id}`)}
              isDefault={s.id === (d.onDeck?.parentId ?? d.children[0]?.id)} sub={seasonSub(g, s)} />
          ) : g && (
            <TvCard key={`g${season}`} c={seasonCard(g, d)} shape="poster" missing menu={false} sub={seasonSub(g)}
              onSelect={() => nav(`/item/${d.id}/season/${season}`)} />
          ))}
        </TvRow>
      )}

      {d.kind === "season" && (
        <>
          <SeasonTabs showId={showId} current={d.seasonNumber} siblings={d.siblings ?? []} guide={guide} />
          <TvRow id={`eps-${d.id}`} title={episodeCount ? `Episodes · ${episodeCount.replace(" episodes", "")}` : "Episodes"}
            extra={<EpisodeBlurb c={focusedEntry ? focusedEp : d.children[0]} status={focusedEntry?.guide && !focusedEntry.card ? focusedEntry.guide : undefined} />}>
            {episodes.map(({ episode, card: e, guide: g }) => e ? (
              <TvCard key={e.id} c={{ ...e, showBackdrop: d.show?.backdrop || d.showBackdrop }} shape="land"
                label={`E${e.episodeNumber ?? ""}`} title={e.title} sub={e.runtime ? fmtRuntime(e.runtime) : ""}
                onFocusCard={setFocusedEp} onSelect={() => play(e.id)} onPlay={() => play(e.id)} isDefault={e.id === target?.id} />
            ) : g && (
              <TvCard key={`g${episode}`} c={episodeCard(g, d, d.show?.backdrop || d.showBackdrop)} shape="land" missing menu={false}
                label={`E${episode}`} title={g.name || `Episode ${episode}`} sub={episodeStatus(g)}
                onFocusCard={setFocusedEp} onSelect={() => toast(missingToast(g))} />
            ))}
          </TvRow>
        </>
      )}

      {d.kind === "episode" && d.siblings && d.siblings.length > 1 && (
        <TvRow id={`sib-${d.parentId}`} title={d.parent?.title || "More episodes"}>
          {d.siblings.map((e) => (
            <TvCard key={e.id} c={{ ...e, showBackdrop: d.showBackdrop }} shape="land" label={`E${e.episodeNumber ?? ""}`}
              title={e.title} sub={e.id === d.id ? "Now viewing" : e.runtime ? fmtRuntime(e.runtime) : ""}
              onSelect={() => nav(`/item/${e.id}`, { replace: true })} onPlay={() => play(e.id)} isDefault={e.id === d.id} />
          ))}
        </TvRow>
      )}
      {isTitle && extras && extras.cast.length > 0 && (
        <section className="tv-row tv-cast-row" data-row data-group={`cast-${d.id}`}>
          <h2 className="tv-row-title">Cast</h2>
          <div className="tv-track tv-cast" data-hscroll>
            {extras.cast.map((p) => (
              <button key={p.name + p.character} className="tv-person" data-f data-fk={`p${p.id || p.name}`}
                onClick={() => (p.id ? nav(`/person/${p.id}`) : toast(`No page for ${p.name} yet`))}>
                <div className="tv-person-photo">
                  {p.profile ? <img src={art(p.profile, "w185")} alt="" loading="lazy" /> : <span>{initials(p.name)}</span>}
                </div>
                <div className="tv-person-name">{p.name}</div>
                {p.character && <div className="tv-person-role">{p.character}</div>}
              </button>
            ))}
          </div>
        </section>
      )}

      {isTitle && extras && extras.similar.length > 0 && (
        <TvRow id={`similar-${d.id}`} title="More like this">
          {extras.similar.map((c) => (
            <TvCard key={c.id} c={c} shape="poster" onSelect={() => nav(`/item/${c.id}`)} onPlay={() => playCard(c).catch((e: Error) => toast(e.message))} />
          ))}
        </TvRow>
      )}
      <div className="tv-page-tail" />
    </div>
  );
}

type Extras = { cast: { id?: number; name: string; character?: string; profile?: string }[]; similar: Card[] };


function EpisodeBlurb({ c, status }: { c?: Card; status?: GuideEpisode }) {
  if (!c) return null;
  return (
    <div className="tv-blurb">
      <div className="tv-blurb-title">
        <span className="tv-ep-label">{episodeLabel(c)}</span>{c.title}
        {c.watched && <span className="tv-watched"><Check size={16} /></span>}
        {status && <span className="tv-blurb-status">{episodeStatus(status)}</span>}
      </div>
      {c.overview && <p>{c.overview}</p>}
    </div>
  );
}

export function missingToast(g: GuideEpisode) {
  if (g.state === "upcoming") return g.airDate ? `Episode ${g.episode} airs on ${fmtAirDate(g.airDate)}` : `Episode ${g.episode} hasn't got an air date yet`;
  return `Episode ${g.episode} isn't in your library`;
}

/** Season tabs on a season page: the library's seasons plus the ones you don't have. */
export function SeasonTabs({ showId, current, siblings, guide }: { showId?: number; current?: number; siblings: Card[]; guide?: Guide }) {
  const nav = useNavigate();
  const tabs = mergeSeasons(siblings, guide);
  if (tabs.length < 2 || !showId) return null;
  return (
    <div className="tv-tabs" data-row data-group="seasons">
      {tabs.map(({ season, card, guide: g }) => {
        const on = season === current;
        const fk = card ? `s${card.id}` : `sg${season}`;
        return (
          <button key={season} data-f data-fk={fk} data-default={on ? "" : undefined}
            className={`tv-tab ${on ? "on" : ""} ${card ? "" : "missing"}`}
            onClick={() => !on && nav(card ? `/item/${card.id}` : `/item/${showId}/season/${season}`, { replace: true, state: { focus: fk } })}>
            {card?.title || g?.name || `Season ${season}`}
          </button>
        );
      })}
    </div>
  );
}
