import { KeyboardEvent, useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Wrapped as WrappedT, wrappedUrl } from "../../api";
import { WrappedStory, wrappedSlides } from "../../components/WrappedStory";
import { useApi } from "../../hooks";
import { Spinner, TvButton } from "../components";
import { focusInitial } from "../focus";

/**
 * Plexodus Wrapped on the TV: the shared story, full screen. Right / OK goes
 * to the next slide, Left to the previous one, Back leaves. Slides also move
 * on by themselves. The last slide has buttons (watch again, other years).
 *
 * Only the current slide is rendered and nothing re-renders on a key press
 * except the slide change itself.
 */
export default function Wrapped() {
  const [year, setYear] = useState<number>();
  const { data, error } = useApi<WrappedT>(wrappedUrl(year));
  const nav = useNavigate();
  const [i, setI] = useState(0);

  const close = useCallback(() => ((window.history.state?.idx ?? 0) > 0 ? nav(-1) : nav("/", { replace: true })), [nav]);

  const end = data && (
    <div className="tv-actions wr-tv-actions">
      <TvButton primary autoFocus fk="wr-again" onClick={() => setI(0)}>Watch again</TvButton>
      {data.years.filter((y) => y !== data.year).slice(0, 3).map((y) => (
        <TvButton key={y} fk={`wr-year-${y}`} onClick={() => setYear(y)}>{String(y)}</TvButton>
      ))}
      <TvButton fk="wr-done" onClick={close}>Done</TvButton>
    </div>
  );
  const slides = data ? wrappedSlides(data, end) : [];
  const n = slides.length;
  const last = i >= n - 1;
  const go = useCallback((d: number) => setI((x) => Math.max(0, Math.min(n - 1, x + d))), [n]);

  useEffect(() => setI(0), [data?.year]);
  // Focus the anchor (or the last slide's first button) whenever the slide changes.
  useEffect(() => {
    if (!data) return;
    const raf = requestAnimationFrame(() => focusInitial());
    return () => cancelAnimationFrame(raf);
  }, [data, last]);

  const onKeyDown = (e: KeyboardEvent) => {
    const fk = (document.activeElement as HTMLElement | null)?.dataset.fk;
    let step = 0;
    if (fk === "wr-next") step = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
    else if (fk === "wr-again" && e.key === "ArrowLeft") step = -1;
    if (e.key === "ArrowUp" || e.key === "ArrowDown") {
      if (fk === "wr-next") {
        e.preventDefault();
        e.stopPropagation();
      }
      return;
    }
    if (!step) return;
    e.preventDefault();
    e.stopPropagation();
    go(step);
  };

  return (
    <div className="tv-wrapped" data-scope onKeyDown={onKeyDown}>
      {error ? (
        <div className="tv-center"><p className="tv-muted">{error}</p></div>
      ) : !data ? (
        <Spinner />
      ) : (
        <>
          <WrappedStory slides={slides} index={i} onDone={() => go(1)} />
          {!last && (
            <button className="tv-wrapped-anchor" data-f data-fk="wr-next" data-autofocus aria-label="Next" onClick={() => go(1)}>
              <span className="tv-wrapped-hint">◀ ▶ to move · Back to leave</span>
            </button>
          )}
        </>
      )}
    </div>
  );
}
