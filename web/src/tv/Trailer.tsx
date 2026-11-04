import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { native } from "../native";

const keys = new Map<number, string>();

/** The title's YouTube trailer key ("" when it has none). */
async function trailerKey(id: number) {
  const known = keys.get(id);
  if (known !== undefined) return known;
  const { youtube } = await api.get<{ youtube: string }>(`/api/items/${id}/trailer`);
  keys.set(id, youtube || "");
  return youtube || "";
}

/**
 * A title's trailer, full-bleed behind the art. It stays invisible until the
 * video is actually playing, then fades in over the art, and fades back out
 * when it ends (calling `onEnd`). If YouTube won't play it (no trailer,
 * embedding blocked), nothing ever shows and the art stays put.
 */
export function TrailerVideo({ id, sound, onEnd, onPlaying }: {
  id: number; sound: boolean; onEnd?: () => void; onPlaying?: () => void;
}) {
  const [key, setKey] = useState<string>();
  const [on, setOn] = useState(false);
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
    let muted = !sound;
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
        if (!ready) clearInterval(listen);
        ready = true;
      }
      let state: number | undefined;
      if (m.event === "onStateChange" && typeof m.info === "number") state = m.info;
      if (m.event === "infoDelivery" && m.info && typeof (m.info as { playerState?: number }).playerState === "number") {
        state = (m.info as { playerState: number }).playerState;
      }
      if (m.event === "onError") state = 0;
      if (state === 1 && !playing) {
        playing = true;
        setOn(true);
        cbs.current.onPlaying?.();
      } else if (state === 0) {
        playing = false;
        setOn(false);
        cbs.current.onEnd?.();
      }
    };
    window.addEventListener("message", onMsg);
    return () => {
      clearInterval(listen);
      clearTimeout(nudge);
      window.removeEventListener("message", onMsg);
    };
    // Sound is read once: changing it mid-trailer would restart the video.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  if (!key) return null;
  const origin = window.location.origin;
  // Browsers outside the app refuse unmuted autoplay outright.
  const mute = sound && native ? 0 : 1;
  const src = `https://www.youtube-nocookie.com/embed/${encodeURIComponent(key)}?autoplay=1&mute=${mute}&controls=0&disablekb=1&fs=0` +
    `&iv_load_policy=3&modestbranding=1&playsinline=1&rel=0&cc_load_policy=0&enablejsapi=1` +
    `&origin=${encodeURIComponent(origin)}&widget_referrer=${encodeURIComponent(origin)}`;
  return (
    <div ref={wrap} className={`tv-trailer ${on ? "on" : ""}`} aria-hidden="true">
      <iframe ref={frame} src={src} title="Trailer" tabIndex={-1} allow="autoplay; encrypted-media"
        referrerPolicy="strict-origin-when-cross-origin" />
    </div>
  );
}
