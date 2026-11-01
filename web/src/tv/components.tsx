import { createContext, ReactNode, useContext, useEffect, useRef, useState } from "react";
import { art, Card, episodeLabel } from "../api";
import { Check, Play } from "../components/Icons";

// ---------- shared backdrop ----------

export const BackdropCtx = createContext<(url?: string) => void>(() => {});
export const useBackdrop = (url: string | undefined, delay = 0) => {
  const set = useContext(BackdropCtx);
  useEffect(() => {
    if (!delay) return set(url);
    const t = setTimeout(() => set(url), delay);
    return () => clearTimeout(t);
  }, [url, set, delay]);
};

/** Full-screen black-and-white backdrop that cross-fades between images. */
export function Backdrop({ url }: { url?: string }) {
  const [layers, setLayers] = useState<{ url: string; key: number; ready: boolean }[]>([]);
  const n = useRef(0);
  useEffect(() => {
    if (!url) {
      setLayers((l) => l.map((x) => ({ ...x, ready: false })));
      return;
    }
    const key = ++n.current;
    const img = new Image();
    img.onload = () => setLayers((l) => [...l.filter((x) => x.key !== key).slice(-1).map((x) => ({ ...x, ready: false })), { url, key, ready: true }]);
    img.src = url;
  }, [url]);
  return (
    <div className="tv-backdrop" aria-hidden="true">
      {layers.map((l) => (
        <div key={l.key} className={`tv-backdrop-img ${l.ready ? "on" : ""}`} style={{ backgroundImage: `url(${l.url})` }} />
      ))}
      <div className="tv-backdrop-shade" />
      <div className="tv-grain" />
    </div>
  );
}

// ---------- images ----------

function hue(s: string) {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) % 360;
  return h;
}

export function Art({ src, title, className }: { src?: string; title: string; className?: string }) {
  const [state, setState] = useState<"loading" | "ok" | "err">(src ? "loading" : "err");
  // Reset when the image changes, during render: an effect would run after a
  // cached image's onLoad and hide it again.
  const [shownSrc, setShownSrc] = useState(src);
  if (shownSrc !== src) {
    setShownSrc(src);
    setState(src ? "loading" : "err");
  }
  return (
    <div className={`tv-art ${className || ""}`} style={{ "--h": hue(title) } as React.CSSProperties}>
      {state !== "ok" && <div className="tv-art-fallback"><span>{state === "err" ? title : ""}</span></div>}
      {src && state !== "err" && (
        <img src={src} alt="" loading="lazy" decoding="async" className={state === "ok" ? "in" : ""}
          onLoad={() => setState("ok")} onError={() => setState("err")} />
      )}
    </div>
  );
}

// ---------- cards ----------

export function cardSub(c: Card) {
  if (c.kind === "episode") return `${episodeLabel(c)}${c.title ? ` · ${c.title}` : ""}`;
  if (c.kind === "season") return `${c.leafCount || 0} episodes`;
  return c.year ? String(c.year) : "";
}

function Badge({ c }: { c: Card }) {
  if (c.kind === "show" || c.kind === "season") {
    const left = (c.leafCount || 0) - (c.viewedLeafCount || 0);
    if (c.leafCount && left === 0) return <span className="tv-badge done"><Check size={14} /></span>;
    if (left > 0 && c.viewedLeafCount) return <span className="tv-badge">{left}</span>;
    return null;
  }
  return c.watched ? <span className="tv-badge done"><Check size={14} /></span> : null;
}

function Progress({ c }: { c: Card }) {
  if (!c.position || !c.duration) return null;
  return <div className="tv-progress"><div style={{ width: `${Math.min(100, (c.position / c.duration) * 100)}%` }} /></div>;
}

export interface CardProps {
  c: Card;
  shape: "poster" | "land";
  onSelect: (c: Card) => void;
  onPlay?: (c: Card) => void;
  onFocusCard?: (c: Card) => void;
  title?: string;
  sub?: string;
  label?: string;
  autoFocus?: boolean;
}

export function TvCard({ c, shape, onSelect, onPlay, onFocusCard, title, sub, label, autoFocus }: CardProps) {
  const isEp = c.kind === "episode";
  const img = shape === "poster"
    ? art(c.poster || c.showPoster, "w342")
    : art(c.backdrop || c.showBackdrop, "w500");
  const t = title ?? (isEp && shape === "land" ? c.showTitle || c.title : c.title);
  const s = sub ?? (isEp && shape === "land" ? cardSub(c) : c.kind === "season" ? cardSub(c) : c.year ? String(c.year) : "");
  return (
    <button
      className={`tv-card ${shape}`}
      data-f
      data-fk={`c${c.id}`}
      data-autofocus={autoFocus ? "" : undefined}
      onClick={() => onSelect(c)}
      onFocus={() => onFocusCard?.(c)}
      onKeyDown={(e) => {
        if (onPlay && (e.key === "MediaPlayPause" || e.key === "MediaPlay" || e.key === "p")) {
          e.preventDefault();
          onPlay(c);
        }
      }}
    >
      <div className="tv-card-frame">
        <Art src={img} title={t} />
        {label && <span className="tv-card-label">{label}</span>}
        <Badge c={c} />
        <Progress c={c} />
        <span className="tv-card-play"><Play size={28} /></span>
      </div>
      <div className="tv-card-cap">
        <div className="t">{t}</div>
        {s && <div className="s">{s}</div>}
      </div>
    </button>
  );
}

// ---------- rows ----------

export function TvRow({ id, title, children, extra }: { id: string; title?: ReactNode; children: ReactNode; extra?: ReactNode }) {
  return (
    <section className="tv-row" data-row data-group={id}>
      {title && <h2 className="tv-row-title">{title}</h2>}
      <div className="tv-track" data-hscroll>{children}</div>
      {extra}
    </section>
  );
}

// ---------- buttons ----------

export function TvButton({ children, primary, onClick, autoFocus, fk, className, icon }: {
  children?: ReactNode; primary?: boolean; onClick: () => void; autoFocus?: boolean; fk?: string; className?: string; icon?: ReactNode;
}) {
  return (
    <button className={`tv-btn ${primary ? "primary" : ""} ${icon && !children ? "icon" : ""} ${className || ""}`} data-f data-fk={fk}
      data-autofocus={autoFocus ? "" : undefined} onClick={onClick}>
      {icon}
      {children && <span>{children}</span>}
    </button>
  );
}

export function Spinner({ label }: { label?: string }) {
  return (
    <div className="tv-loading">
      <div className="tv-spinner" />
      {label && <div className="tv-loading-label">{label}</div>}
    </div>
  );
}
