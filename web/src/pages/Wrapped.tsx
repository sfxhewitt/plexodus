import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Wrapped as WrappedT, wrappedUrl } from "../api";
import { ChevronLeft, ChevronRight } from "../components/Icons";
import { WrappedStory, wrappedSlides } from "../components/WrappedStory";
import { useApi } from "../hooks";

/**
 * Plexodus Wrapped, web: a full-screen story. Tap the right side (or swipe
 * left, or press →) for the next slide, the left side for the previous one;
 * hold to pause. Slides move on by themselves.
 */
export default function Wrapped() {
  const [params, setParams] = useSearchParams();
  const year = Number(params.get("year")) || undefined;
  const { data, error } = useApi<WrappedT>(wrappedUrl(year));
  const nav = useNavigate();
  const [i, setI] = useState(0);
  const [paused, setPaused] = useState(false);
  const down = useRef<{ x: number; y: number; t: number } | null>(null);

  const close = useCallback(() => ((window.history.state?.idx ?? 0) > 0 ? nav(-1) : nav("/")), [nav]);

  const end = data && (
    <div className="wr-actions" onPointerDown={(e) => e.stopPropagation()} onClick={(e) => e.stopPropagation()}>
      <button className="btn primary" onClick={() => setI(0)}>Watch again</button>
      {data.years.filter((y) => y !== data.year).slice(0, 3).map((y) => (
        <button key={y} className="btn glass" onClick={() => setParams({ year: String(y) }, { replace: true })}>{y}</button>
      ))}
      <button className="btn glass" onClick={close}>Done</button>
    </div>
  );
  const slides = data ? wrappedSlides(data, end) : [];
  const n = slides.length;
  const go = useCallback((d: number) => setI((x) => Math.max(0, Math.min(n - 1, x + d))), [n]);

  useEffect(() => setI(0), [data?.year]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "ArrowRight" || e.key === " ") go(1);
      else if (e.key === "ArrowLeft") go(-1);
      else if (e.key === "Escape") close();
      else return;
      e.preventDefault();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [go, close]);

  if (error) return <div className="wr-page"><div className="page-msg">{error}</div></div>;
  if (!data) return <div className="wr-page"><div className="page-loading full"><div className="spinner" /></div></div>;

  return (
    <div className="wr-page"
      onPointerDown={(e) => {
        down.current = { x: e.clientX, y: e.clientY, t: Date.now() };
        setPaused(true);
      }}
      onPointerUp={(e) => {
        setPaused(false);
        const d = down.current;
        down.current = null;
        if (!d) return;
        const dx = e.clientX - d.x;
        if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(e.clientY - d.y)) return go(dx < 0 ? 1 : -1); // swipe
        if (Date.now() - d.t > 400) return; // a hold just paused
        go(e.clientX < window.innerWidth * 0.3 ? -1 : 1);
      }}
      onPointerCancel={() => {
        setPaused(false);
        down.current = null;
      }}>
      <WrappedStory slides={slides} index={i} paused={paused} onDone={() => go(1)} />
      <button className="wr-close icon-btn" aria-label="Close" onPointerDown={(e) => e.stopPropagation()} onPointerUp={(e) => e.stopPropagation()} onClick={close}>✕</button>
      {i > 0 && <button className="wr-nav prev icon-btn" aria-label="Previous" onPointerDown={(e) => e.stopPropagation()} onPointerUp={(e) => e.stopPropagation()} onClick={() => go(-1)}><ChevronLeft /></button>}
      {i < n - 1 && <button className="wr-nav next icon-btn" aria-label="Next" onPointerDown={(e) => e.stopPropagation()} onPointerUp={(e) => e.stopPropagation()} onClick={() => go(1)}><ChevronRight /></button>}
    </div>
  );
}
