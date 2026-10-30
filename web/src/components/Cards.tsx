import { useRef, useState } from "react";
import { Link } from "react-router-dom";
import { art, Card, episodeLabel } from "../api";
import { Check, ChevronLeft, ChevronRight, Play } from "./Icons";

function hue(s: string) {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) % 360;
  return h;
}

/** Image that fades in once loaded and falls back to a generated tile. */
export function Img({ src, alt, title, className }: { src?: string; alt: string; title: string; className?: string }) {
  const [state, setState] = useState<"loading" | "ok" | "err">(src ? "loading" : "err");
  const h = hue(title);
  return (
    <div className={`img ${className || ""}`} style={{ "--h": h } as React.CSSProperties}>
      {state !== "ok" && (
        <div className="img-fallback">
          <span>{state === "err" ? title : ""}</span>
        </div>
      )}
      {src && state !== "err" && (
        <img src={src} alt={alt} loading="lazy" decoding="async" className={state === "ok" ? "in" : ""}
          onLoad={() => setState("ok")} onError={() => setState("err")} />
      )}
    </div>
  );
}

export function itemLink(c: Card) {
  return `/item/${c.id}`;
}

function Progress({ c }: { c: Card }) {
  if (!c.position || !c.duration) return null;
  return (
    <div className="progress">
      <div style={{ width: `${Math.min(100, (c.position / c.duration) * 100)}%` }} />
    </div>
  );
}

function Badge({ c }: { c: Card }) {
  if (c.kind === "show" || c.kind === "season") {
    const left = (c.leafCount || 0) - (c.viewedLeafCount || 0);
    if (c.leafCount && left === 0) return <span className="badge done"><Check size={12} /></span>;
    if (left > 0 && c.viewedLeafCount) return <span className="badge">{left}</span>;
    return null;
  }
  if (c.watched) return <span className="badge done"><Check size={12} /></span>;
  return null;
}

export function PosterCard({ c, subtitle }: { c: Card; subtitle?: string }) {
  const sub = subtitle ?? (c.kind === "season" ? `${c.leafCount || 0} episodes` : c.year ? String(c.year) : "");
  return (
    <Link to={itemLink(c)} className="poster-card">
      <div className="poster-frame">
        <Img src={art(c.poster || c.showPoster, "w342")} alt={c.title} title={c.title} />
        <Badge c={c} />
        <Progress c={c} />
        <div className="poster-hover"><span className="play-dot"><Play size={20} /></span></div>
      </div>
      <div className="poster-meta">
        <div className="t">{c.title}</div>
        {sub && <div className="s">{sub}</div>}
      </div>
    </Link>
  );
}

export function LandscapeCard({ c, to }: { c: Card; to?: string }) {
  const isEp = c.kind === "episode";
  const img = art(c.backdrop || c.showBackdrop, "w780");
  const title = isEp ? c.showTitle || c.title : c.title;
  const sub = isEp ? `${episodeLabel(c)} — ${c.title}` : c.year ? String(c.year) : "";
  return (
    <Link to={to || (c.position ? `/play/${c.id}` : itemLink(c))} className="land-card">
      <div className="land-frame">
        <Img src={img} alt={title} title={title} />
        <div className="land-shade" />
        <div className="land-text">
          <div className="t">{title}</div>
          <div className="s">{sub}</div>
        </div>
        <Progress c={c} />
        <div className="poster-hover"><span className="play-dot"><Play size={22} /></span></div>
      </div>
    </Link>
  );
}

export function Row({ title, link, kind, items }: { title: string; link?: string; kind: "poster" | "landscape"; items: Card[] }) {
  const ref = useRef<HTMLDivElement>(null);
  const scroll = (dir: number) => {
    const el = ref.current;
    if (el) el.scrollBy({ left: dir * el.clientWidth * 0.85, behavior: "smooth" });
  };
  return (
    <section className={`row row-${kind}`}>
      <div className="row-head">
        {link ? <Link to={link} className="row-title">{title}<ChevronRight size={18} /></Link> : <h2 className="row-title">{title}</h2>}
        <div className="row-arrows">
          <button className="icon-btn" onClick={() => scroll(-1)} aria-label="Scroll left"><ChevronLeft /></button>
          <button className="icon-btn" onClick={() => scroll(1)} aria-label="Scroll right"><ChevronRight /></button>
        </div>
      </div>
      <div className="row-track" ref={ref}>
        {items.map((c) => (kind === "poster" ? <PosterCard key={c.id} c={c} /> : <LandscapeCard key={c.id} c={c} />))}
      </div>
    </section>
  );
}
