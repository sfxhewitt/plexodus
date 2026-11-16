import { useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { art, Card, Detail, episodeLabel } from "../../api";
import { Tv } from "../../components/Icons";
import { useApi } from "../../hooks";
import { episodeCard, episodeStatus, fmtAirDate, Guide } from "../../guide";
import { Spinner, TvButton, TvCard, TvRow, useBackdrop } from "../components";
import { usePageFocus, useToast } from "../hooks";
import { missingToast, SeasonTabs } from "./Item";

/** A season that isn't in the library: its episodes from TMDB, greyed out, with air dates. */
export default function GuideSeason() {
  const { id, n } = useParams();
  const season = Number(n);
  const nav = useNavigate();
  const toast = useToast();
  const { data: show, error } = useApi<Detail>(`/api/items/${id}`);
  const { data: guide, error: gErr } = useApi<Guide>(`/api/items/${id}/guide?season=${season}`);
  const [focused, setFocused] = useState<Card>();
  const ready = !!show && !!guide && String(show.id) === id && guide.season === season;
  useBackdrop(art(show?.backdrop, "w1280"));
  usePageFocus(ready);

  if (error || gErr) return <div className="tv-center"><p className="tv-muted">{error || gErr}</p></div>;
  if (!ready) return <Spinner />;

  const g = guide.seasons.find((s) => s.season === season);
  const eps = guide.episodes;
  const cards = eps.map((e) => episodeCard(e, show, show.backdrop));
  const blurb = cards.find((c) => c.id === focused?.id) ?? cards[0];
  const blurbEp = eps[cards.indexOf(blurb)];
  const aired = eps.filter((e) => e.state === "missing").length;
  const meta = [
    g?.airDate ? g.airDate.slice(0, 4) : "",
    eps.length ? `${eps.length} episode${eps.length === 1 ? "" : "s"}` : "",
    aired ? "Not in your library" : g?.nextAirDate || g?.airDate ? `Coming ${fmtAirDate(g.nextAirDate || g.airDate)}` : "Coming soon",
  ].filter(Boolean);

  return (
    <div className="tv-page tv-detail kind-season" data-vscroll="reveal" data-margin="4rem">
      <section className="tv-detail-hero" data-row data-row-top>
        <div className="tv-detail-info">
          <div className="tv-eyebrow">{show.title}</div>
          <h1 className="tv-detail-title">{g?.name || `Season ${season}`}</h1>
          <div className="tv-meta">{meta.map((m) => <span key={m}>{m}</span>)}</div>
          {!guide.available && guide.message && <p className="tv-overview">{guide.message}</p>}
          <div className="tv-actions" data-group="actions">
            <TvButton autoFocus fk="show" icon={<Tv size={22} />} onClick={() => nav(`/item/${show.id}`)}>Go to show</TvButton>
          </div>
        </div>
      </section>
      <SeasonTabs showId={show.id} current={season} siblings={show.children} guide={guide} />
      {cards.length > 0 && (
        <TvRow id={`geps-${show.id}-${season}`} title="Episodes"
          extra={blurb && (
            <div className="tv-blurb">
              <div className="tv-blurb-title">
                <span className="tv-ep-label">{episodeLabel(blurb)}</span>{blurb.title}
                {blurbEp && <span className="tv-blurb-status">{episodeStatus(blurbEp)}</span>}
              </div>
              {blurb.overview && <p>{blurb.overview}</p>}
            </div>
          )}>
          {eps.map((e, i) => (
            <TvCard key={e.episode} c={cards[i]} shape="land" missing menu={false} label={`E${e.episode}`}
              title={cards[i].title} sub={episodeStatus(e)} onFocusCard={setFocused} onSelect={() => toast(missingToast(e))} />
          ))}
        </TvRow>
      )}
      <div className="tv-page-tail" />
    </div>
  );
}
