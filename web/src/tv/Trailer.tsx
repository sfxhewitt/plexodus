import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { native } from "../native";
import { onBack } from "./focus";

const keys = new Map<number, string>();

/** Whether the title is known to have a trailer (its key has been fetched). */
export const hasTrailerKey = (id: number) => !!keys.get(id);

/** The title's YouTube trailer key ("" when it has none). */
export async function trailerKey(id: number) {
  const known = keys.get(id);
  if (known !== undefined) return known;
  const { youtube } = await api.get<{ youtube: string }>(`/api/items/${id}/trailer`);
  keys.set(id, youtube || "");
  return youtube || "";
}

function unmute(send: (m: object) => void) {
  send({ event: "command", func: "unMute", args: [] });
  send({ event: "command", func: "setVolume", args: [100] });
}

/**
 * A title's trailer, full-bleed behind the art. It stays invisible until the
 * video is actually playing, then fades in over the art, and fades back out
 * when it ends (calling `onEnd`). If YouTube won't play it (no trailer,
 * embedding blocked), nothing ever shows and the art stays put.
 */
export function TrailerVideo({ id, sound, onEnd, onPlaying, full = false, onExitFull }: {
  id: number; sound: boolean; onEnd?: () => void; onPlaying?: () => void;
  /**
   * Full screen with sound: OK pauses, Left/Right skip 10 s, Back or Down
   * calls `onExitFull`, and Up calls it with "up" (go on to the tab bar).
   */
  full?: boolean; onExitFull?: (dir?: "up") => void;
}) {
  const [key, setKey] = useState<string>();
  const [on, setOn] = useState(false);
  const [paused, setPaused] = useState(false);
  // A trailer opened straight to full screen starts with sound.
  const [startSound] = useState(sound || full);
  const fullRef = useRef(full);
  fullRef.current = full;
  // The live player: how to talk to it and what it last told us.
  const ctl = useRef({ send: (_: object) => {}, playing: false, paused: false, time: 0 });
  const frame = useRef<HTMLIFrameElement>(null);
  const wrap = useRef<HTMLDivElement>(null);
  const cbs = useRef({ onEnd, onPlaying });
  cbs.current = { onEnd, onPlaying };

  useEffect(() => {
    let live = true;
    setKey(undefined);
    setOn(false);
    trailerKey(id).then((k) => live && setKey(k), () => {});
    return () => {
      live = false;
    };
  }, [id]);

  // The frame must never take the remote's keys: keep it out of focus.
  useEffect(() => {
    wrap.current?.setAttribute("inert", "");
    const prev = { el: document.activeElement as HTMLElement | null };
    const onFocusIn = (e: FocusEvent) => {
      if (e.target instanceof HTMLElement && e.target.tagName !== "IFRAME") prev.el = e.target;
    };
    const onBlur = () => {
      setTimeout(() => {
        if (document.activeElement === frame.current) prev.el?.focus({ preventScroll: true });
      }, 0);
    };
    document.addEventListener("focusin", onFocusIn);
    window.addEventListener("blur", onBlur);
    return () => {
      document.removeEventListener("focusin", onFocusIn);
      window.removeEventListener("blur", onBlur);
    };
  }, []);

  // Talk to the YouTube player over postMessage (its iframe API, without
  // loading YouTube's script into our page).
  useEffect(() => {
    if (!key) return;
    const send = (msg: object) => frame.current?.contentWindow?.postMessage(JSON.stringify(msg), "*");
    let ready = false;
    let playing = false;
    let muted = !startSound;
    ctl.current.send = send;
    const listen = setInterval(() => send({ event: "listening", id: 1, channel: "widget" }), 250);
    // An unmuted autoplay can be refused (desktop browsers): fall back to muted.
    const nudge = setTimeout(() => {
      if (!playing && !muted) {
        muted = true;
        send({ event: "command", func: "mute", args: [] });
        send({ event: "command", func: "playVideo", args: [] });
      }
    }, 4000);
    const onMsg = (e: MessageEvent) => {
      if (e.source !== frame.current?.contentWindow || typeof e.data !== "string") return;
      let m: { event?: string; info?: unknown };
      try {
        m = JSON.parse(e.data);
      } catch {
        return;
      }
      if (m.event === "onReady" || m.event === "initialDelivery") {
        if (!ready) {
          clearInterval(listen);
          if (fullRef.current) unmute(send);
        }
        ready = true;
      }
      const t = (m.info as { currentTime?: number } | undefined)?.currentTime;
      if (m.event === "infoDelivery" && typeof t === "number") ctl.current.time = t;
      let state: number | undefined;
      if (m.event === "onStateChange" && typeof m.info === "number") state = m.info;
      if (m.event === "infoDelivery" && m.info && typeof (m.info as { playerState?: number }).playerState === "number") {
        state = (m.info as { playerState: number }).playerState;
      }
      if (m.event === "onError") state = 0;
      if (state === 1 || state === 2) {
        ctl.current.paused = state === 2;
        setPaused(state === 2);
      }
      if (state === 1 && !playing) {
        playing = true;
        ctl.current.playing = true;
        setOn(true);
        cbs.current.onPlaying?.();
      } else if (state === 0) {
        playing = false;
        ctl.current.playing = false;
        setOn(false);
        cbs.current.onEnd?.();
      }
    };
    window.addEventListener("message", onMsg);
    return () => {
      clearInterval(listen);
      clearTimeout(nudge);
      window.removeEventListener("message", onMsg);
      ctl.current.send = () => {};
    };
    // Sound is read once: changing it mid-trailer would restart the video.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  // Full screen: sound on, and the remote drives the trailer instead of the page.
  const exitRef = useRef(onExitFull);
  exitRef.current = onExitFull;
  useEffect(() => {
    if (!full) {
      if (!sound) ctl.current.send({ event: "command", func: "mute", args: [] });
      return;
    }
    const c = ctl.current;
    unmute(c.send);
    if (c.paused) c.send({ event: "command", func: "playVideo", args: [] });
    const exit = (dir?: "up") => exitRef.current?.(dir);
    const offBack = onBack(() => {
      exit();
      return true;
    });
    const onKey = (e: KeyboardEvent) => {
      const k = e.key;
      const ok = k === "Enter" || e.keyCode === 23 || k === "MediaPlayPause" || k === " ";
      const back = k === "Escape" || k === "GoBack" || k === "BrowserBack" || k === "Backspace" || k === "ArrowDown";
      const skip = k === "ArrowLeft" ? -10 : k === "ArrowRight" ? 10 : 0;
      const up = k === "ArrowUp";
      if (!ok && !back && !skip && !up) return;
      // Keep it from the page underneath (focus moves, button presses).
      e.preventDefault();
      e.stopPropagation();
      if (e.type !== "keydown") return;
      if (back || up) {
        if (!e.repeat) exit(up ? "up" : undefined);
      } else if (ok) {
        if (!e.repeat) c.send({ event: "command", func: c.paused ? "playVideo" : "pauseVideo", args: [] });
      } else if (skip) {
        c.time = Math.max(0, c.time + skip);
        c.send({ event: "command", func: "seekTo", args: [c.time, true] });
      }
    };
    window.addEventListener("keydown", onKey, true);
    window.addEventListener("keyup", onKey, true);
    return () => {
      offBack();
      window.removeEventListener("keydown", onKey, true);
      window.removeEventListener("keyup", onKey, true);
    };
  }, [full, sound]);

  if (!key) return null;
  const origin = window.location.origin;
  // Browsers outside the app refuse unmuted autoplay outright.
  const mute = startSound && native ? 0 : 1;
  const src = `https://www.youtube-nocookie.com/embed/${encodeURIComponent(key)}?autoplay=1&mute=${mute}&controls=0&disablekb=1&fs=0` +
    `&iv_load_policy=3&modestbranding=1&playsinline=1&rel=0&cc_load_policy=0&enablejsapi=1` +
    `&origin=${encodeURIComponent(origin)}&widget_referrer=${encodeURIComponent(origin)}`;
  return (
    <div ref={wrap} className={`tv-trailer ${on ? "on" : ""} ${full ? "full" : ""}`} aria-hidden="true">
      <iframe ref={frame} src={src} title="Trailer" tabIndex={-1} allow="autoplay; encrypted-media"
        referrerPolicy="strict-origin-when-cross-origin" />
      {full && (
        <div key={paused ? "p" : on ? "on" : "load"} className={`tv-trailer-keys ${paused || !on ? "stay" : ""}`}>
          {paused ? "Paused · OK to play" : !on ? "Loading trailer…" : "OK pause · ◀ ▶ skip · Back to return"}
        </div>
      )}
    </div>
  );
}
