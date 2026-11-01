import { useNavigate } from "react-router-dom";
import { useState } from "react";
import { art, Card, Detail, episodeLabel, fmtRuntime, Row as RowT } from "../../api";
import { ChevronRight } from "../../components/Icons";
import { useApi, useDebounced } from "../../hooks";
import { Spinner, TvCard, TvRow, useBackdrop } from "../components";
import { usePageFocus, useRefresh, useToast } from "../hooks";
import { playItem } from "../play";

/** The focused title's details, shown above the rows like the Plex TV home screen. */
export function Spotlight({ c, compact }: { c?: Card; compact?: boolean }) {
  const wantDetail = c && (c.kind === "movie" || c.kind === "show");
  const { data: d } = useApi<Detail>(wantDetail ? `/api/items/${c!.id}` : null);
  if (!c) return <div className="tv-spotlight" />;
  const isEp = c.kind === "episode";
  const detail = d && d.id === c.id ? d : undefined;
  const overview = detail?.overview || c.overview;
  const logo = !isEp && (detail?.logo || c.logo);
  const meta = [
    isEp ? episodeLabel(c) : c.kind === "show" ? "Series" : c.kind === "season" ? "Season" : "Film",
    c.year ? String(c.year) : "",
    c.runtime ? fmtRuntime(c.runtime) : "",
    c.kind === "show" && c.leafCount ? `${c.leafCount} episodes` : "",
  ].filter(Boolean);
  return (
    <div className={`tv-spotlight ${compact ? "compact" : ""}`} key={c.id}>
      {logo ? (
        <img className="tv-spot-logo" src={art(logo, "w500")} alt={c.title} />
      ) : (
        <h1 className="tv-spot-title">{isEp ? c.showTitle || c.title : c.title}</h1>
      )}
      <div className="tv-meta">
        {c.rating ? <span className="tv-rating">★ {c.rating.toFixed(1)}</span> : null}
        {meta.map((m) => <span key={m}>{m}</span>)}
        {detail?.genres.slice(0, 3).map((g) => <span key={g} className="tv-genre">{g}</span>)}
      </div>
      {isEp && <div className="tv-spot-ep">{c.title}</div>}
      {overview && <p className="tv-spot-overview">{overview}</p>}
    </div>
  );
}

export default function Home() {
  const { data, error, reload } = useApi<{ hero: Card[]; rows: RowT[] }>("/api/home");
  useRefresh(reload);
  const nav = useNavigate();
  const toast = useToast();
  const [focused, setFocused] = useState<Card>();
  const shown = useDebounced(focused, 180);
  const first = data?.rows.find((r) => r.items.length)?.items[0];
  const spot = shown || first;
  useBackdrop(art(spot ? spot.backdrop || spot.showBackdrop : undefined, "w1280"), 120);
  usePageFocus(!!data);

  if (error) return <div className="tv-center"><p className="tv-muted">{error}</p></div>;
  if (!data) return <Spinner />;

  const rows = data.rows.filter((r) => r.items.length);
  if (!rows.length) {
    return (
      <div className="tv-center">
        <h1 className="tv-h1">Nothing here yet</h1>
        <p className="tv-muted">Add a library from the Plexodus web page and it will show up here.</p>
      </div>
    );
  }

  const play = (c: Card) => playItem(c.id).catch((e: Error) => toast(e.message));
  const select = (c: Card, landscape: boolean) => {
    if (landscape && (c.kind === "episode" || c.kind === "movie") && c.position) play(c);
    else nav(`/item/${c.id}`);
  };

  return (
    <div className="tv-home">
      <Spotlight c={spot} />
      <div className="tv-home-rows" data-vscroll="pin" data-pin="0">
        {rows.map((r, ri) => {
          const land = r.kind === "landscape";
          return (
            <TvRow key={r.title} id={`home-${r.title}`} title={r.title}>
              {r.items.map((c, i) => (
                <TvCard key={c.id} c={c} shape={land ? "land" : "poster"} autoFocus={ri === 0 && i === 0}
                  onSelect={(x) => select(x, land)} onPlay={play} onFocusCard={setFocused} />
              ))}
              {r.link && (
                <button className={`tv-card more ${land ? "land" : "poster"}`} data-f data-fk={`more-${r.title}`} onClick={() => nav(r.link!)}>
                  <div className="tv-card-frame"><div className="tv-more"><ChevronRight size={40} /><span>See all</span></div></div>
                </button>
              )}
            </TvRow>
          );
        })}
        <div className="tv-rows-tail" />
      </div>
    </div>
  );
}
