import { Fragment, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api, art, Card, episodeLabel, fmtRuntime, HomeData, Library, logoArt, suggestions } from "../api";
import { Row } from "../components/Cards";
import { Info, Play, X } from "../components/Icons";
import { useApi, useUser } from "../hooks";
import { hiddenOnHome, hideOnHome, hideSuggestionOnHome, homeRows, onHomeStale, playerJustClosed, unhideAllOnHome, unhideOnHome } from "../homeHidden";
import { hasRT, RTScores } from "../rt";

function Hero({ items }: { items: Card[] }) {
  const [i, setI] = useState(0);
  useEffect(() => {
    if (items.length < 2) return;
    const t = setInterval(() => setI((n) => (n + 1) % items.length), 9000);
    return () => clearInterval(t);
  }, [items.length, i]);
  if (!items.length) return null;
  const c = items[i];
  return (
    <section className="hero">
      {items.map((h, n) => (
        <div key={h.id} className={`hero-bg ${n === i ? "on" : ""}`}
          style={{ backgroundImage: `url(${art(h.backdrop, window.innerWidth < 900 ? "w780" : "w1280")})` }} />
      ))}
      <div className="hero-shade" />
      <div className="hero-content" key={c.id}>
        {c.logo ? (
          <img className="hero-logo" src={logoArt(c.logo)} alt={c.title} />
        ) : (
          <h1 className="hero-title">{c.title}</h1>
        )}
        <div className="hero-meta">
          {hasRT(c) ? <RTScores c={c} /> : c.rating ? <span className="pill">★ {c.rating.toFixed(1)}</span> : null}
          {c.year ? <span>{c.year}</span> : null}
          {c.runtime ? <span>{fmtRuntime(c.runtime)}</span> : null}
          <span className="cap">{c.kind === "show" ? "Series" : "Film"}</span>
        </div>
        {c.overview && <p className="hero-overview">{c.overview}</p>}
        <div className="hero-actions">
          <Link to={c.kind === "movie" ? `/play/${c.id}` : `/item/${c.id}`} className="btn primary"><Play size={18} /> Play</Link>
          <Link to={`/item/${c.id}`} className="btn glass"><Info size={18} /> More info</Link>
        </div>
      </div>
      <div className="hero-dots">
        {items.map((h, n) => (
          <button key={h.id} className={n === i ? "on" : ""} onClick={() => setI(n)} aria-label={`Show ${h.title}`} />
        ))}
      </div>
    </section>
  );
}

function WrappedBanner({ year }: { year: number }) {
  return (
    <Link to="/wrapped" className="wrapped-banner">
      <span className="wrapped-banner-glow" />
      <span className="wrapped-banner-text">
        <span className="wrapped-banner-eyebrow">Plexodus Wrapped</span>
        <span className="wrapped-banner-title">Your {year} in watching</span>
        <span className="wrapped-banner-sub">Hours, top shows, binge records and your peak time of day</span>
      </span>
      <span className="wrapped-banner-year">{year}</span>
    </Link>
  );
}

/**
 * What you get by pressing and holding a card on Home (or right-clicking
 * it): take it off Up Next, or stop it being suggested.
 */
function CardSheet({ c, upNext, onRemove, onClose }: { c: Card; upNext: boolean; onRemove: () => void; onClose: () => void }) {
  useEffect(() => {
    const key = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", key);
    document.body.classList.add("sheet-open");
    return () => {
      window.removeEventListener("keydown", key);
      document.body.classList.remove("sheet-open");
    };
  }, [onClose]);
  const isEp = c.kind === "episode";
  const sub = isEp ? `${episodeLabel(c)} · ${c.title}` : c.note || (c.year ? String(c.year) : "");
  return (
    <div className="card-sheet-back" data-testid="card-sheet" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div className="card-sheet" role="dialog" aria-modal="true" aria-label={c.title}>
        <div className="card-sheet-head">
          <b>{isEp ? c.showTitle || c.title : c.title}</b>
          {sub && <span className="muted small">{sub}</span>}
        </div>
        <button className="card-sheet-item" data-testid="sheet-remove" onClick={onRemove}>
          <X size={20} />
          <span>
            <b>{upNext ? "Remove from Up Next" : "Remove from suggestions"}</b>
            <span className="muted small">{upNext ? "It comes back if you watch it again." : "It won't be suggested to you again. It stays in your library."}</span>
          </span>
        </button>
        <Link className="card-sheet-item" to={`/item/${c.id}`} onClick={onClose}>
          <Info size={20} />
          <span><b>Details</b></span>
        </Link>
        <button className="card-sheet-item cancel" onClick={onClose}><span><b>Cancel</b></span></button>
      </div>
    </div>
  );
}

export default function Home() {
  const { data, error, reload } = useApi<HomeData>("/api/home");
  const { data: libs } = useApi<Library[]>("/api/libraries");
  const { user } = useUser();
  // Back from the player: Home was likely fetched before the player's last
  // progress report was saved, so look again once it has been.
  useEffect(() => {
    if (!playerJustClosed()) return;
    const t = [2500, 7000].map((ms) => setTimeout(reload, ms));
    return () => t.forEach(clearTimeout);
  }, [reload]);
  useEffect(() => onHomeStale(reload), [reload]);
  // Something finished or removed leaves Home at once.
  const [, setHiddenTick] = useState(0);
  useEffect(() => {
    const on = () => setHiddenTick((t) => t + 1);
    window.addEventListener("tv:home-hidden", on);
    return () => window.removeEventListener("tv:home-hidden", on);
  }, []);

  // Press and hold a card: remove it from Up Next or from suggestions. The
  // card leaves at once; the server keeps it out from then on.
  const [sheet, setSheet] = useState<{ c: Card; upNext: boolean }>();
  const [note, setNote] = useState<{ text: string; undo?: () => void }>();
  const [removedHere, setRemovedHere] = useState(0);
  const noteTimer = useRef<ReturnType<typeof setTimeout>>();
  const say = (text: string, undo?: () => void) => {
    clearTimeout(noteTimer.current);
    setNote({ text, undo });
    noteTimer.current = setTimeout(() => setNote(undefined), undo ? 7000 : 3500);
  };
  useEffect(() => () => clearTimeout(noteTimer.current), []);
  const remove = async () => {
    if (!sheet) return;
    const { c, upNext } = sheet;
    setSheet(undefined);
    try {
      if (upNext) {
        hideOnHome(c.id);
        await api.post(`/api/items/${c.id}/continue/hide`);
        say("Removed from Up Next");
        return;
      }
      hideSuggestionOnHome(c.id);
      await suggestions.hide(c.id);
      setRemovedHere((n) => n + 1);
      say("Removed from your suggestions", async () => {
        setNote(undefined);
        await suggestions.restore(c.id).catch(() => {});
        unhideOnHome(c.id);
        setRemovedHere((n) => Math.max(0, n - 1));
      });
    } catch (e) {
      unhideOnHome(c.id);
      say((e as Error).message);
    }
  };
  const restoreAll = async () => {
    try {
      await suggestions.restoreAll();
      unhideAllOnHome();
      setRemovedHere(0);
      say("They can all be suggested again");
      reload();
    } catch (e) {
      say((e as Error).message);
    }
  };

  if (error) return <div className="page-msg">{error}</div>;
  if (!data) return <div className="page-loading"><div className="spinner" /></div>;

  if (libs && libs.length === 0) {
    return (
      <div className="empty-state">
        <div className="empty-glow" />
        <h1>Welcome to Plexodus</h1>
        <p>Point it at your media folders and it will index them in the background.</p>
        {user?.isAdmin ? <Link to="/settings" className="btn primary">Add a library</Link> : <p>Ask an admin to add a library.</p>}
      </div>
    );
  }

  return (
    <div className={`home ${data.hero.length ? "" : "no-hero"}`}>
      <Hero items={data.hero.filter((c) => !hiddenOnHome(c.id))} />
      <div className="rows">
        {homeRows(data.rows).map((r, i) => (
          <Fragment key={r.title}>
            {/* The Watchlist has its own button on each title, so its row has no sheet. */}
            <Row {...r} onHold={r.title === "Watchlist" ? undefined : (c) => setSheet({ c, upNext: r.kind === "landscape" })} />
            {/* Wrapped sits under the first row, so Up Next stays on top. */}
            {i === 0 && data.wrapped && <WrappedBanner year={data.wrapped.year} />}
          </Fragment>
        ))}
        {!data.rows.length && data.wrapped && <WrappedBanner year={data.wrapped.year} />}
        {(data.removed || 0) + removedHere > 0 && (
          <p className="home-removed muted small" data-testid="home-removed">
            Titles you removed from your suggestions stay off this page.{" "}
            <button className="home-link" onClick={restoreAll}>Bring them back</button>
          </p>
        )}
      </div>
      {sheet && <CardSheet c={sheet.c} upNext={sheet.upNext} onRemove={remove} onClose={() => setSheet(undefined)} />}
      {note && (
        <div className="home-note" role="status" data-testid="home-note">
          <span>{note.text}</span>
          {note.undo && <button className="home-link" onClick={note.undo}>Undo</button>}
        </div>
      )}
    </div>
  );
}
