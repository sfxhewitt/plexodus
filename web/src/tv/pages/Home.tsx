import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { art, Card, fmtRuntime, Row as RowT } from "../../api";
import { ChevronRight, Info, Play } from "../../components/Icons";
import { useApi } from "../../hooks";
import { Spinner, TvButton, TvCard, TvRow, useBackdrop } from "../components";
import { usePageFocus, useRefresh, useToast } from "../hooks";
import { playCard, playItem } from "../play";

/**
 * Full-screen featured carousel, like the top of the Apple TV app. Left on
 * the first button and Right on the last flip between titles; it also moves
 * on by itself every few seconds while you're not touching it.
 */
function Hero({ items, onPlay }: { items: Card[]; onPlay: (c: Card) => void }) {
  const nav = useNavigate();
  const [i, setI] = useState(0);
  const touched = useRef(0);
  const n = items.length;

  useEffect(() => {
    if (n < 2) return;
    const t = setInterval(() => {
      if (Date.now() - touched.current > 9000) setI((x) => (x + 1) % n);
    }, 9000);
    return () => clearInterval(t);
  }, [n]);

  const c = items[i % n];
  const meta = [
    c.kind === "show" ? "Series" : "Film",
    c.year ? String(c.year) : "",
    c.runtime ? fmtRuntime(c.runtime) : "",
  ].filter(Boolean);

  return (
    <section className="tv-hero" data-row data-row-top data-group="hero"
      onKeyDown={(e) => {
        touched.current = Date.now();
        const fk = (document.activeElement as HTMLElement | null)?.dataset.fk;
        const step = e.key === "ArrowLeft" && fk === "hero-play" ? -1 : e.key === "ArrowRight" && fk === "hero-info" ? 1 : 0;
        if (step && n > 1) {
          e.preventDefault();
          e.stopPropagation();
          setI((x) => (x + step + n) % n);
        }
      }}>
      {items.map((h, k) => (
        <div key={h.id} className={`tv-hero-img ${k === i % n ? "on" : ""}`}
          style={{ backgroundImage: h.backdrop ? `url(${art(h.backdrop, "w1280")})` : undefined }} />
      ))}
      <div className="tv-hero-shade" />
      <div className="tv-hero-content">
        {/* Only the text is keyed (to replay its fade): the buttons must survive a flip or focus is lost. */}
        <div className="tv-hero-text" key={c.id}>
          {c.logo ? (
            <img className="tv-hero-logo" src={art(c.logo, "w500")} alt={c.title} />
          ) : (
            <h1 className="tv-hero-title">{c.title}</h1>
          )}
          <div className="tv-meta">
            {c.rating ? <span className="tv-rating">★ {c.rating.toFixed(1)}</span> : null}
            {meta.map((m) => <span key={m}>{m}</span>)}
          </div>
          {c.overview && <p className="tv-hero-overview">{c.overview}</p>}
        </div>
        <div className="tv-actions">
          <TvButton primary autoFocus fk="hero-play" icon={<Play size={24} />} onClick={() => onPlay(c)}>
            {c.position && c.duration ? "Resume" : "Play"}
          </TvButton>
          <TvButton fk="hero-info" icon={<Info size={24} />} onClick={() => nav(`/item/${c.id}`)}>Details</TvButton>
        </div>
      </div>
      {n > 1 && (
        <div className="tv-hero-dots">
          {items.map((h, k) => <span key={h.id} className={k === i % n ? "on" : ""} />)}
        </div>
      )}
    </section>
  );
}

export default function Home() {
  const { data, error, reload } = useApi<{ hero: Card[]; rows: RowT[] }>("/api/home");
  useRefresh(reload);
  const nav = useNavigate();
  const toast = useToast();
  useBackdrop(undefined);
  usePageFocus(!!data);

  const play = useCallback((c: Card) => playItem(c.id).catch((e: Error) => toast(e.message)), [toast]);
  const playAny = useCallback((c: Card) => playCard(c).catch((e: Error) => toast(e.message)), [toast]);

  if (error) return <div className="tv-center"><p className="tv-muted">{error}</p></div>;
  if (!data) return <Spinner />;

  const rows = data.rows.filter((r) => r.items.length);
  if (!rows.length && !data.hero.length) {
    return (
      <div className="tv-center">
        <h1 className="tv-h1">Nothing here yet</h1>
        <p className="tv-muted">Add a library from the Plexodus web page and it will show up here.</p>
      </div>
    );
  }

  const select = (c: Card, continuing: boolean) => {
    if (continuing && (c.kind === "episode" || c.kind === "movie") && c.position) play(c);
    else nav(`/item/${c.id}`);
  };
  const hasHero = data.hero.length > 0;

  return (
    <div className={`tv-page tv-home ${hasHero ? "" : "no-hero"}`} data-vscroll="pin" data-pin="2.5rem">
      {hasHero && <Hero items={data.hero} onPlay={playAny} />}
      <div className="tv-home-rows">
        {rows.map((r, ri) => {
          // Apple TV style: wide cards everywhere; posters only when there's no art.
          const continuing = r.kind === "landscape";
          return (
            <TvRow key={r.title} id={`home-${r.title}`} title={r.title}>
              {r.items.map((c, i) => (
                <TvCard key={c.id} c={c} shape="land" autoFocus={!hasHero && ri === 0 && i === 0}
                  title={c.kind === "episode" ? c.showTitle || c.title : c.title}
                  onSelect={(x) => select(x, continuing)} onPlay={play} />
              ))}
              {r.link && (
                <button className="tv-card more land" data-f data-fk={`more-${r.title}`} onClick={() => nav(r.link!)}>
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
