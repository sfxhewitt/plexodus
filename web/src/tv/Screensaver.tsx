import { useCallback, useEffect, useRef, useState } from "react";
import { api, art, Card } from "../api";
import { Play } from "../components/Icons";
import { ambientPrefs, onAmbientPrefs, onStartScreensaver, stopTrailer } from "./ambient";
import { onBack, swallowSelectUntilRelease } from "./focus";
import { onPlayBusy, playCard } from "./play";

const SLIDE_MS = 14000;
const isSelect = (e: KeyboardEvent) => e.key === "Enter" || e.keyCode === 23;

function clock() {
  return new Date().toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

/**
 * When the TV sits untouched, drift slowly through full-screen art from the
 * library, with each title and a hint that OK plays it. Any other button
 * wakes the app back where you left it. The TV's own screensaver still takes
 * over after its timeout, so the screen isn't kept on for ever.
 */
export function Screensaver({ onError }: { onError: (msg: string) => void }) {
  const [active, setActive] = useState(false);
  const activeRef = useRef(false);
  activeRef.current = active;
  const armRef = useRef<() => void>();

  // ---- idle timer ----
  useEffect(() => {
    let t: ReturnType<typeof setTimeout> | undefined;
    // While the app's player is up, the page isn't idle.
    let playing = false;
    const arm = () => {
      clearTimeout(t);
      const min = ambientPrefs().screensaver;
      if (!min || playing || activeRef.current) return;
      t = setTimeout(() => {
        if (!document.hidden && !playing) setActive(true);
      }, min * 60000);
    };
    const onKey = () => {
      playing = false;
      arm();
    };
    const onVis = () => !document.hidden && arm();
    const onEnded = () => {
      playing = false;
      arm();
    };
    window.addEventListener("keydown", onKey, true);
    window.addEventListener("pointermove", onKey, true);
    document.addEventListener("visibilitychange", onVis);
    window.addEventListener("plexodus:native-ended", onEnded);
    const offBusy = onPlayBusy((b) => {
      if (b) {
        playing = true;
        clearTimeout(t);
      }
    });
    const offPrefs = onAmbientPrefs(arm);
    const offStart = onStartScreensaver(() => setActive(true));
    armRef.current = arm;
    arm();
    return () => {
      clearTimeout(t);
      window.removeEventListener("keydown", onKey, true);
      window.removeEventListener("pointermove", onKey, true);
      document.removeEventListener("visibilitychange", onVis);
      window.removeEventListener("plexodus:native-ended", onEnded);
      offBusy();
      offPrefs();
      offStart();
    };
  }, []);

  // Closing restarts the idle countdown.
  useEffect(() => {
    if (!active) armRef.current?.();
  }, [active]);
  const close = useCallback(() => setActive(false), []);

  return active ? <Slides onClose={close} onError={onError} /> : null;
}

function Slides({ onClose, onError }: { onClose: () => void; onError: (msg: string) => void }) {
  const [items, setItems] = useState<Card[]>([]);
  const [i, setI] = useState(0);
  const prev = useRef<Card>();
  const [loaded, setLoaded] = useState<Set<number>>(() => new Set());
  const [time, setTime] = useState(clock);
  const current = useRef<Card>();

  useEffect(() => {
    stopTrailer();
    api.get<Card[]>("/api/screensaver").then((cs) => setItems(cs.filter((c) => c.backdrop)), () => onClose());
  }, [onClose]);

  // Preload the next picture so every change is a clean cross-fade.
  useEffect(() => {
    if (!items.length) return;
    const want = [i % items.length, (i + 1) % items.length];
    for (const k of want) {
      const c = items[k];
      if (loaded.has(c.id)) continue;
      const img = new Image();
      img.onload = () => setLoaded((s) => new Set(s).add(c.id));
      img.onerror = () => setItems((xs) => xs.filter((x) => x.id !== c.id));
      img.src = art(c.backdrop, "w1280")!;
    }
  }, [items, i, loaded]);

  useEffect(() => {
    if (items.length < 2) return;
    const t = setInterval(() => {
      setI((x) => {
        const next = (x + 1) % items.length;
        // Hold on this slide until the next picture has arrived.
        return loaded.has(items[next].id) ? next : x;
      });
    }, SLIDE_MS);
    return () => clearInterval(t);
  }, [items, loaded]);

  useEffect(() => {
    const t = setInterval(() => setTime(clock()), 15000);
    return () => clearInterval(t);
  }, []);

  // Keys: OK plays what's on screen, anything else just wakes the app.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      e.preventDefault();
      e.stopImmediatePropagation();
      if (isSelect(e)) {
        // Its release must not also press whatever is focused underneath.
        swallowSelectUntilRelease();
        const c = current.current;
        onClose();
        if (c) playCard(c).catch((err: Error) => onError(err.message));
        return;
      }
      onClose();
    };
    window.addEventListener("keydown", onKey, true);
    const offBack = onBack(() => (onClose(), true));
    return () => {
      window.removeEventListener("keydown", onKey, true);
      offBack();
    };
  }, [onClose, onError]);

  const shown = items.length ? items[i % items.length] : undefined;
  const c = shown && loaded.has(shown.id) ? shown : undefined;
  // Only the outgoing and incoming pictures are drawn: the new one fades in on top.
  if (c && current.current && current.current !== c) prev.current = current.current;
  current.current = c;
  const layers = [prev.current !== c ? prev.current : undefined, c].filter((x): x is Card => !!x);

  return (
    <div className="tv-saver" data-scope aria-label="Screensaver">
      {layers.map((x) => (
        <div key={x.id} className={`tv-saver-img ${items.indexOf(x) % 2 ? "alt" : ""}`}
          style={{ backgroundImage: `url(${art(x.backdrop, "w1280")})` }} />
      ))}
      <div className="tv-saver-shade" />
      <div className="tv-saver-clock">{time}</div>
      {c && (
        <div className="tv-saver-info" key={c.id}>
          {c.logo ? <img className="tv-saver-logo" src={art(c.logo, "w500")} alt={c.title} /> : <div className="tv-saver-title">{c.title}</div>}
          <div className="tv-saver-meta">
            {[c.kind === "show" ? "Series" : "Film", c.year ? String(c.year) : ""].filter(Boolean).join(" · ")}
          </div>
          <div className="tv-saver-hint"><span className="tv-saver-ok"><Play size={16} /> OK</span> to play</div>
        </div>
      )}
    </div>
  );
}
