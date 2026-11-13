import { Fragment, useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { art, Card, episodeLabel, fmtRuntime, HomeData } from "../../api";
import { ChevronRight, Info, Play } from "../../components/Icons";
import { useApi } from "../../hooks";
import { Spinner, TvButton, TvCard, TvRow, useBackdrop } from "../components";
import { usePageFocus, useRefresh, useToast } from "../hooks";
import { ambientPrefs, trailersSupported } from "../ambient";
import { onPlayBusy, playCard, playItem } from "../play";
import { TrailerVideo } from "../Trailer";
import { focusInitial } from "../focus";

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
  const c = items[i % n];

  // Rest on the hero and the title's trailer fades in behind it; when it
  // ends the carousel moves on to the next title (and its trailer).
  const [trailer, setTrailer] = useState<number>();
  const [trailing, setTrailing] = useState(false);
  const inHero = useRef(false);
  const tTimer = useRef<ReturnType<typeof setTimeout>>();
  const stop = useCallback(() => {
    clearTimeout(tTimer.current);
    setTrailer(undefined);
    setTrailing(false);
  }, []);
  const arm = useCallback((id: number) => {
    stop();
    if (!ambientPrefs().trailers || !trailersSupported()) return;
    tTimer.current = setTimeout(() => inHero.current && !document.hidden && setTrailer(id), 3000);
  }, [stop]);
  useEffect(() => {
    if (inHero.current) arm(c.id);
  }, [c.id, arm]);
  useEffect(() => onPlayBusy((b) => b && stop()), [stop]);
  useEffect(() => {
    const vis = () => document.hidden && stop();
    document.addEventListener("visibilitychange", vis);
    return () => {
      document.removeEventListener("visibilitychange", vis);
      clearTimeout(tTimer.current);
    };
  }, [stop]);
  const playingRef = useRef(false);
  playingRef.current = trailing;

  useEffect(() => {
    if (n < 2) return;
    const t = setInterval(() => {
      if (!playingRef.current && Date.now() - touched.current > 9000) setI((x) => (x + 1) % n);
    }, 9000);
    return () => clearInterval(t);
  }, [n]);

  const meta = [
    c.kind === "show" ? "Series" : "Film",
    c.year ? String(c.year) : "",
    c.runtime ? fmtRuntime(c.runtime) : "",
  ].filter(Boolean);

  return (
    <section className={`tv-hero ${trailing ? "trailing" : ""}`} data-row data-row-top data-group="hero"
      onFocus={() => {
        if (inHero.current) return;
        inHero.current = true;
        arm(c.id);
      }}
      onBlur={(e) => {
        const sec = e.currentTarget;
        setTimeout(() => {
          if (sec.contains(document.activeElement)) return;
          inHero.current = false;
          stop();
        }, 0);
      }}
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
      {trailer !== undefined && (
        <TrailerVideo key={trailer} id={trailer} sound={ambientPrefs().trailerSound} onPlaying={() => setTrailing(true)}
          onEnd={() => {
            stop();
            touched.current = 0;
            if (n > 1) setI((x) => (x + 1) % n);
            else inHero.current = false;
          }} />
      )}
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

const CONTINUE = { continueRow: true };

/** "S2 · E4 · 12m left" on a Continue watching card: where you are and how long is left. */
function continueSub(c: Card) {
  const left = c.position && c.duration && !c.watched ? Math.round((c.duration - c.position) / 60) : 0;
  const where = c.kind === "episode" ? episodeLabel(c) : "";
  const rest = left > 0 ? `${fmtRuntime(Math.max(1, left))} left` : c.kind === "episode" ? c.title : c.year ? String(c.year) : "";
  return [where, rest].filter(Boolean).join(" · ") || undefined;
}

/** A single wide tile that opens Plexodus Wrapped. */
function WrappedBanner({ year }: { year: number }) {
  const nav = useNavigate();
  return (
    <section className="tv-row tv-wrapped-row" data-row data-group="home-wrapped">
      <button className="tv-wrapped-banner" data-f data-fk="wrapped" onClick={() => nav("/wrapped")}>
        <span className="tv-wrapped-glow" />
        <span className="tv-wrapped-text">
          <span className="tv-wrapped-eyebrow">Plexodus Wrapped</span>
          <span className="tv-wrapped-title">Your {year} in watching</span>
          <span className="tv-wrapped-sub">Hours, top shows, binge records and your peak time of day</span>
        </span>
        <span className="tv-wrapped-year">{year}</span>
      </button>
    </section>
  );
}

export default function Home() {
  const { data, error, reload } = useApi<HomeData>("/api/home");
  useRefresh(reload);
  const nav = useNavigate();
  const toast = useToast();
  useBackdrop(undefined);
  usePageFocus(!!data);
  // The error screen's "Try again" gets focus, so the remote can reach it.
  useEffect(() => {
    if (error) requestAnimationFrame(() => focusInitial());
  }, [error]);

  const play = useCallback((c: Card) => playItem(c.id).catch((e: Error) => toast(e.message)), [toast]);
  const playAny = useCallback((c: Card) => playCard(c).catch((e: Error) => toast(e.message)), [toast]);

  if (error) {
    return (
      <div className="tv-center">
        <h1 className="tv-h1">Home didn't load</h1>
        <p className="tv-muted">{error}</p>
        <div className="tv-actions">
          <TvButton primary autoFocus onClick={reload}>Try again</TvButton>
        </div>
      </div>
    );
  }
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
            <Fragment key={r.title}>
            <TvRow id={`home-${r.title}`} title={r.smart ? <><span className="tv-row-eyebrow">Tonight</span>{r.title}</> : r.title}>
              {r.items.map((c, i) => (
                <TvCard key={c.id} c={c} shape="land" autoFocus={!hasHero && ri === 0 && i === 0}
                  title={c.kind === "episode" ? c.showTitle || c.title : c.title}
                  sub={continuing ? continueSub(c) : undefined}
                  onSelect={(x) => select(x, continuing)} onPlay={play} menu={continuing ? CONTINUE : undefined} />
              ))}
              {r.link && (
                <button className="tv-card more land" data-f data-fk={`more-${r.title}`} onClick={() => nav(r.link!)}>
                  <div className="tv-card-frame"><div className="tv-more"><ChevronRight size={40} /><span>See all</span></div></div>
                </button>
              )}
            </TvRow>
            {/* Wrapped sits under the first row, so Up Next stays on top. */}
            {ri === 0 && data.wrapped && <WrappedBanner year={data.wrapped.year} />}
            </Fragment>
          );
        })}
        <div className="tv-rows-tail" />
      </div>
    </div>
  );
}
