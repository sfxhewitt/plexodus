import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import Hls from "hls.js";
import { api, Card, Detail, episodeLabel, fmtTime, Marker, MediaFile, PlayInfo } from "../api";
import { Back, Copy, External, Forward, Fullscreen, Mute, Pause, Play, Rewind, Volume } from "../components/Icons";
import { native, NativeEnded, nativePlay } from "../native";

// Plexodus never re-encodes video. The browser gets the file as it is when
// it can play it. When it can't (Safari has no MKV support, few browsers
// decode DTS), the server remuxes the same streams into HLS, converting only
// the audio to AAC if the browser can't decode it. Anything else goes to an
// external player.
const videoCodecs: Record<string, string> = {
  h264: 'video/mp4; codecs="avc1.640028"',
  hevc: 'video/mp4; codecs="hvc1.1.6.L120.90"',
  av1: 'video/mp4; codecs="av01.0.08M.08"',
  vp9: 'video/webm; codecs="vp9"',
  vp8: 'video/webm; codecs="vp8"',
};
const audioCodecs: Record<string, string> = {
  aac: 'audio/mp4; codecs="mp4a.40.2"',
  mp3: "audio/mpeg",
  opus: 'audio/webm; codecs="opus"',
  vorbis: 'audio/webm; codecs="vorbis"',
  flac: 'audio/mp4; codecs="flac"',
  ac3: 'audio/mp4; codecs="ac-3"',
  eac3: 'audio/mp4; codecs="ec-3"',
  dts: 'audio/mp4; codecs="dtsc"',
  truehd: 'audio/mp4; codecs="mlpa"',
};
// Audio that can ride along unchanged inside fragmented MP4 (HLS).
const hlsAudioCopy = new Set(["aac", "mp3", "ac3", "eac3", "flac"]);
// Video that can be copied into fragmented MP4. VP8/VP9 belong in WebM.
const hlsVideoCopy = new Set(["h264", "hevc", "av1"]);
const badContainers = ["avi", "asf", "flv", "mpeg", "mpegts"];
const directContainers = ["mov", "mp4", "m4a", "3gp", "3g2", "mj2", "webm", "matroska", "ogg"];

// WebKit (Safari, and every browser on iOS) has no Matroska demuxer, so an
// .mkv can never direct play there regardless of its codecs. Knowing that
// from the file name saves waiting for the <video> element to fail.
const webkitOnly = /AppleWebKit/.test(navigator.userAgent) && !/Chrome|Chromium|Edg\//.test(navigator.userAgent);
const nativeHLS = !!document.createElement("video").canPlayType("application/vnd.apple.mpegurl");
const neverDirect: Record<string, string> = {
  mkv: "Safari can't play MKV files directly",
  avi: "AVI files can't play in a browser",
  wmv: "WMV files can't play in a browser",
  flv: "FLV files can't play in a browser",
  ts: "MPEG-TS files can't play in a browser",
  m2ts: "MPEG-TS files can't play in a browser",
  mpg: "MPEG files can't play in a browser",
  mpeg: "MPEG files can't play in a browser",
};
function notDirectByName(name: string): string | undefined {
  const ext = name.toLowerCase().split(".").pop() || "";
  if (ext === "mkv") return webkitOnly ? neverDirect.mkv : undefined;
  return neverDirect[ext];
}

type Plan =
  | { mode: "direct"; audioWarn?: string }
  | { mode: "hls"; audioMode: "copy" | "aac"; audioIndex?: number }
  | { mode: "none"; reason: string };

function plan(f: MediaFile, name: string, hlsAvailable: boolean): Plan {
  const probe = document.createElement("video");
  const can = (t?: string) => !!t && probe.canPlayType(t) !== "";
  if (!f.probed) return { mode: "direct" };
  const container = (f.container || "").split(",")[0];
  const v = !f.videoCodec || can(videoCodecs[f.videoCodec]);
  const a = !f.audioCodec || can(audioCodecs[f.audioCodec]) || f.audioCodec.startsWith("pcm");
  const byName = notDirectByName(name);
  const containerOK = !byName && !badContainers.includes(container) && (directContainers.includes(container) || !container);

  if (containerOK && v && a) return { mode: "direct" };
  if (containerOK && v && !a && !hlsAvailable) {
    return { mode: "direct", audioWarn: `This browser can't decode ${f.audioCodec?.toUpperCase()} audio` };
  }
  if (hlsAvailable && f.videoCodec && hlsVideoCopy.has(f.videoCodec) && v) {
    const audio = (f.streams || []).filter((s) => s.type === "audio");
    const track = audio.find((s) => s.default) || audio[0];
    const codec = track?.codec || f.audioCodec || "";
    const copy = hlsAudioCopy.has(codec) && can(audioCodecs[codec]);
    return { mode: "hls", audioMode: copy ? "copy" : "aac", audioIndex: track?.index };
  }
  if (!v) return { mode: "none", reason: `This browser can't decode ${f.videoCodec?.toUpperCase()} video` };
  if (byName) return { mode: "none", reason: byName + (hlsAvailable ? "" : ", and the server has no ffmpeg to convert it") };
  if (!a) return { mode: "none", reason: `This browser can't decode ${f.audioCodec?.toUpperCase()} audio` };
  return { mode: "none", reason: `${container.toUpperCase() || "This"} files can't play in a browser` };
}

function ExternalPlayers({ info }: { info: PlayInfo }) {
  const [copied, setCopied] = useState(false);
  const url = info.externalUrl;
  const m3u = `data:audio/x-mpegurl;charset=utf-8,${encodeURIComponent(`#EXTM3U\n#EXTINF:-1,${info.fileName}\n${url}\n`)}`;
  const copy = async () => {
    await navigator.clipboard?.writeText(url);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <div className="ext-players">
      <a className="btn glass sm" href={`vlc://${url}`}><External size={16} /> VLC</a>
      <a className="btn glass sm" href={`infuse://x-callback-url/play?url=${encodeURIComponent(url)}`}><External size={16} /> Infuse</a>
      <a className="btn glass sm" href={m3u} download={`${info.fileName}.m3u`}><External size={16} /> Playlist (.m3u)</a>
      <button className="btn glass sm" onClick={copy}><Copy size={16} /> {copied ? "Copied" : "Copy stream URL"}</button>
    </div>
  );
}

interface HlsSession {
  id: string;
  url: string;
  start: number;
  // A VOD stream has its full playlist up front: the player seeks anywhere
  // natively and `start` is just the position to resume at. Otherwise the
  // stream begins at `start` and seeking outside it needs a new stream.
  vod: boolean;
}

export default function Player() {
  const { id } = useParams();
  const [params] = useSearchParams();
  const nav = useNavigate();
  const video = useRef<HTMLVideoElement>(null);
  const wrap = useRef<HTMLDivElement>(null);
  const [item, setItem] = useState<Detail>();
  const [info, setInfo] = useState<PlayInfo>();
  const [error, setError] = useState<string>();
  const [unsupported, setUnsupported] = useState<string>();
  const [audioWarn, setAudioWarn] = useState<string>();
  const [playing, setPlaying] = useState(false);
  const [time, setTime] = useState(0);
  const [dur, setDur] = useState(0);
  const [buffered, setBuffered] = useState(0);
  const [waiting, setWaiting] = useState(true);
  const [muted, setMuted] = useState(false);
  const [volume, setVolume] = useState(() => Number(localStorage.getItem("plexodus.volume") ?? 1));
  const [idle, setIdle] = useState(false);
  const [upNext, setUpNext] = useState<Card | null>(null);
  const [countdown, setCountdown] = useState(0);
  const lastReport = useRef(0);
  const [markers, setMarkers] = useState<Marker[]>([]);
  const nativeMode = !!native;

  // Browser fallback (HLS remux) state. `session.start` is where in the
  // file the stream begins, so absolute time = start + video.currentTime.
  const [hlsPlan, setHlsPlan] = useState<Extract<Plan, { mode: "hls" }>>();
  const [preparing, setPreparing] = useState(false);
  const pendingSeek = useRef<number>();
  const [session, setSession] = useState<HlsSession>();
  const hlsRef = useRef<Hls>();
  const sessionRef = useRef<HlsSession>();
  sessionRef.current = session;
  const inHls = !!session;

  const stopSession = useCallback((s?: HlsSession) => {
    hlsRef.current?.destroy();
    hlsRef.current = undefined;
    if (s) {
      const body = new Blob([], { type: "application/json" });
      if (!navigator.sendBeacon?.(`/api/hls/${s.id}/stop`, body)) fetch(`/api/hls/${s.id}/stop`, { method: "POST", keepalive: true }).catch(() => {});
    }
  }, []);

  const startSession = useCallback(async (p: Extract<Plan, { mode: "hls" }>, fileId: number, start: number) => {
    stopSession(sessionRef.current);
    setWaiting(true);
    setPreparing(true);
    const s = await api.post<HlsSession>("/api/hls", { fileId, start, audioMode: p.audioMode, audioIndex: p.audioIndex });
    setSession(s);
    setTime(s.start);
    setBuffered(s.start);
  }, [stopSession]);

  // Load item + play info.
  useEffect(() => {
    let cancelled = false;
    setInfo(undefined);
    setError(undefined);
    setUnsupported(undefined);
    setAudioWarn(undefined);
    setUpNext(null);
    setWaiting(true);
    setSession(undefined);
    setHlsPlan(undefined);
    (async () => {
      try {
        const d = await api.get<Detail>(`/api/items/${id}`);
        if (cancelled) return;
        setItem(d);
        setMarkers([]);
        api.get<Marker[]>(`/api/items/${d.id}/markers`).then((m) => !cancelled && setMarkers(m), () => {});
        if (!d.files.length) throw new Error("No media file for this item.");
        const file = d.files[0];
        const byName = native ? undefined : notDirectByName(file.name || "");
        // A file that can never direct play here is only worth probing if
        // the server can convert it; ask without the probe first.
        let p = await api.get<PlayInfo>(`/api/play/${file.id}${byName ? "?probe=0" : ""}`);
        if (cancelled) return;
        if (byName && !p.hls) {
          setInfo(p);
          setUnsupported(byName + ", and the server has no ffmpeg to convert it");
          return;
        }
        if (byName && !p.file.probed) {
          p = await api.get<PlayInfo>(`/api/play/${file.id}`);
          if (cancelled) return;
        }
        if (native) {
          // The app's player handles decoding, so skip the browser checks.
          const m = await api.get<Marker[]>(`/api/items/${d.id}/markers`).catch(() => [] as Marker[]);
          if (cancelled) return;
          const from = params.get("from");
          const t = d.kind === "episode" ? d.showTitle || d.show?.title || d.title : d.title;
          const st = d.kind === "episode" ? `${episodeLabel(d)} · ${d.title}` : d.year ? String(d.year) : "";
          setInfo(p);
          nativePlay({
            itemId: d.id, kind: d.kind, url: p.externalUrl, title: t, subtitle: st,
            startSeconds: from != null ? Number(from) : p.position || 0,
            durationSeconds: p.file.duration || 0,
            markers: m.map((x) => ({ type: x.type, start: x.start, end: x.end })),
          });
          return;
        }
        const pl = plan(p.file, file.name || p.fileName, p.hls);
        setInfo(p);
        if (pl.mode === "none") {
          setUnsupported(pl.reason);
        } else if (pl.mode === "hls") {
          setHlsPlan(pl);
          setDur(p.file.duration || 0);
          const from = params.get("from");
          const start = from != null ? Number(from) : p.position || 0;
          await startSession(pl, file.id, start > 0 && start < (p.file.duration || Infinity) - 5 ? start : 0);
        } else if (pl.audioWarn) {
          setAudioWarn(pl.audioWarn);
        }
      } catch (e) {
        if (!cancelled) setError((e as Error).message);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [id]);

  // Attach the HLS stream to the <video>; keep the server session alive
  // while the page is open and end it when the player closes.
  useEffect(() => {
    const v = video.current;
    if (!session || !v) return;
    if (nativeHLS) {
      v.src = session.url;
    } else if (Hls.isSupported()) {
      const h = new Hls({ startPosition: session.vod && session.start > 0 ? session.start : 0, maxBufferLength: 60, backBufferLength: 30 });
      hlsRef.current = h;
      h.on(Hls.Events.ERROR, (_e, data) => {
        if (data.fatal) setUnsupported("The server couldn't convert this file for the browser");
      });
      h.loadSource(session.url);
      h.attachMedia(v);
    } else {
      setUnsupported("This browser can't play converted streams");
      return;
    }
    const ping = setInterval(() => fetch(`/api/hls/${session.id}/ping`, { method: "POST" }).catch(() => {}), 30000);
    return () => {
      clearInterval(ping);
      stopSession(session);
    };
  }, [session, stopSession]);

  // iOS suspends background tabs; if the server dropped the stream in the
  // meantime, start a fresh one from where we were when the tab returns.
  useEffect(() => {
    if (!session) return;
    const onVisible = async () => {
      if (session.vod || document.visibilityState !== "visible" || !video.current || video.current.ended) return;
      const r = await fetch(`/api/hls/${session.id}/ping`, { method: "POST" }).catch(() => undefined);
      if (!r) return;
      const { running, finished } = (await r.json().catch(() => ({}))) as { running?: boolean; finished?: boolean };
      if (!running && !finished && hlsPlan && item) {
        startSession(hlsPlan, item.files[0].id, session.start + video.current.currentTime).catch(() => {});
      }
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => document.removeEventListener("visibilitychange", onVisible);
  }, [session, hlsPlan, item, startSession]);

  // Absolute position in the file (the HLS stream may start mid-file).
  const offset = session && !session.vod ? session.start : 0;
  const absTime = () => offset + (video.current?.currentTime || 0);

  const report = useCallback(
    (force = false) => {
      const v = video.current;
      if (!v || !item) return;
      const duration = inHls ? dur : v.duration;
      if (!duration || !isFinite(duration)) return;
      const now = Date.now();
      if (!force && now - lastReport.current < 10000) return;
      lastReport.current = now;
      const body = JSON.stringify({ position: offset + v.currentTime, duration });
      if (force && navigator.sendBeacon) {
        navigator.sendBeacon(`/api/items/${item.id}/progress`, new Blob([body], { type: "application/json" }));
      } else {
        fetch(`/api/items/${item.id}/progress`, { method: "POST", body, headers: { "Content-Type": "application/json" } });
      }
    },
    [item, inHls, dur, offset],
  );

  useEffect(() => {
    const onHide = () => report(true);
    window.addEventListener("pagehide", onHide);
    return () => {
      window.removeEventListener("pagehide", onHide);
      report(true);
      api.invalidate("/api/home");
      api.invalidate("/api/items");
    };
  }, [report]);

  // Start position: resume unless ?from=0.
  const onLoaded = () => {
    const v = video.current!;
    v.volume = volume;
    if (inHls) {
      setPreparing(false);
      if (session?.vod) {
        if (isFinite(v.duration)) setDur(v.duration);
        if (session.start > 0 && session.start < v.duration - 5 && Math.abs(v.currentTime - session.start) > 1) v.currentTime = session.start;
      } else if (pendingSeek.current != null) {
        // The stream already starts at the resume point.
        const t = pendingSeek.current - offset;
        pendingSeek.current = undefined;
        if (t > 0) v.currentTime = t;
      }
      v.play().catch(() => setPlaying(false));
      return;
    }
    setDur(v.duration);
    const from = params.get("from");
    const start = from != null ? Number(from) : info?.position || 0;
    if (start > 0 && start < v.duration - 5) v.currentTime = start;
    // Some browsers "play" unsupported video as audio only.
    if (info?.file.videoCodec && v.videoWidth === 0) {
      setUnsupported(`This browser can't decode ${info.file.videoCodec.toUpperCase()} video`);
      return;
    }
    v.play().catch(() => setPlaying(false));
  };

  const onEnded = async () => {
    report(true);
    if (item?.kind !== "episode" || !item.showId) return;
    // Give the server a moment to record the watch before asking what's next.
    await new Promise((r) => setTimeout(r, 400));
    const show = await api.get<Detail>(`/api/items/${item.showId}`);
    if (show.onDeck && show.onDeck.id !== item.id) {
      setUpNext(show.onDeck);
      setCountdown(10);
    }
  };

  // The app's player reports back when it closes.
  useEffect(() => {
    if (!native) return;
    const onNative = (e: Event) => {
      const d = (e as CustomEvent<NativeEnded>).detail;
      if (!item || d.itemId !== item.id) return;
      api.invalidate("/api/home");
      api.invalidate("/api/items");
      if (d.ended && item.kind === "episode" && item.showId) onEnded();
      else nav(-1);
    };
    window.addEventListener("plexodus:native-ended", onNative);
    return () => window.removeEventListener("plexodus:native-ended", onNative);
  });

  useEffect(() => {
    if (!upNext) return;
    if (countdown <= 0) {
      nav(`/play/${upNext.id}`, { replace: true });
      return;
    }
    const t = setTimeout(() => setCountdown((c) => c - 1), 1000);
    return () => clearTimeout(t);
  }, [upNext, countdown, nav]);

  // Auto-hide controls.
  const idleRef = useRef(false);
  const revealTap = useRef(false);
  useEffect(() => {
    let t: ReturnType<typeof setTimeout>;
    const wake = () => {
      setIdle(false);
      idleRef.current = false;
      clearTimeout(t);
      t = setTimeout(() => {
        setIdle(true);
        idleRef.current = true;
      }, 3000);
    };
    // On a touch screen a tap while the controls are hidden should only
    // bring them back, not also pause the video under the finger.
    const touch = () => {
      revealTap.current = idleRef.current;
      wake();
    };
    wake();
    window.addEventListener("mousemove", wake);
    window.addEventListener("touchstart", touch);
    window.addEventListener("keydown", wake);
    return () => {
      clearTimeout(t);
      window.removeEventListener("mousemove", wake);
      window.removeEventListener("touchstart", touch);
      window.removeEventListener("keydown", wake);
    };
  }, []);

  const toggle = () => {
    const v = video.current;
    if (!v) return;
    if (v.paused) v.play();
    else v.pause();
  };
  const onVideoClick = () => {
    if (revealTap.current) {
      revealTap.current = false;
      return;
    }
    toggle();
  };
  // Leave the player. There isn't always a page to go back to (a link
  // opened straight into the player), so fall back to the item's page.
  const goBack = () => {
    const idx = (window.history.state as { idx?: number } | null)?.idx ?? 0;
    if (idx > 0) nav(-1);
    else nav(item ? `/item/${item.id}` : "/", { replace: true });
  };
  // Seek to an absolute position in the file. A converted stream can only
  // seek within what the server has produced so far; anything else starts
  // a new stream from that point.
  const seekTo = (target: number) => {
    const v = video.current;
    if (!v) return;
    const total = inHls ? dur : v.duration || 0;
    target = Math.max(0, Math.min(total || target, target));
    if (!inHls || sessionRef.current?.vod) {
      v.currentTime = target;
      return;
    }
    const s = sessionRef.current!;
    const loadedEnd = v.seekable.length ? s.start + v.seekable.end(v.seekable.length - 1) : s.start;
    if (target >= s.start && target < loadedEnd - 1) {
      v.currentTime = target - s.start;
    } else if (!v.seekable.length && target >= s.start && target < s.start + 60) {
      // Nothing has arrived yet; apply the seek once it has rather than
      // restarting the conversion for a few seconds' difference.
      pendingSeek.current = target;
    } else if (hlsPlan && item) {
      report(true);
      startSession(hlsPlan, item.files[0].id, target).catch((e) => setError((e as Error).message));
    }
    setTime(target);
  };
  const seek = (delta: number) => seekTo(absTime() + delta);
  const fullscreen = () => {
    if (document.fullscreenElement) document.exitFullscreen();
    else wrap.current?.requestFullscreen?.();
  };
  const setVol = (val: number) => {
    const v = video.current;
    setVolume(val);
    localStorage.setItem("plexodus.volume", String(val));
    if (v) {
      v.volume = val;
      v.muted = val === 0;
      setMuted(val === 0);
    }
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement).tagName === "INPUT") return;
      switch (e.key) {
        case " ":
        case "k":
          e.preventDefault();
          toggle();
          break;
        case "ArrowLeft":
        case "j":
          seek(-10);
          break;
        case "ArrowRight":
        case "l":
          seek(10);
          break;
        case "ArrowUp":
          e.preventDefault();
          setVol(Math.min(1, volume + 0.1));
          break;
        case "ArrowDown":
          e.preventDefault();
          setVol(Math.max(0, volume - 0.1));
          break;
        case "f":
          fullscreen();
          break;
        case "m":
          if (video.current) {
            video.current.muted = !video.current.muted;
            setMuted(video.current.muted);
          }
          break;
        case "Escape":
          if (!document.fullscreenElement) goBack();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  const activeMarker = markers.find((m) => time >= m.start && time < m.end - 1);
  const skip = () => {
    if (!activeMarker) return;
    if (activeMarker.type === "credits" && item?.kind === "episode") {
      video.current?.pause();
      onEnded();
    } else {
      seekTo(activeMarker.end);
    }
  };
  const endsAt = dur ? new Date(Date.now() + ((dur - time) * 1000) / (video.current?.playbackRate || 1)) : null;

  const title = item?.kind === "episode" ? item.showTitle || item.show?.title : item?.title;
  const subtitle = item?.kind === "episode" ? `${episodeLabel(item)} · ${item.title}` : item?.year ? String(item.year) : "";

  if (error) {
    return (
      <div className="player-msg">
        <p>{error}</p>
        <button className="btn glass" onClick={goBack}>Go back</button>
      </div>
    );
  }

  if (nativeMode) {
    return (
      <div className="player-msg">
        {!info && <div className="spinner" />}
        <p>{info ? "Playing in the Plexodus player…" : "Preparing…"}</p>
        {upNext && (
          <div className="up-next static">
            <div className="muted small">Up next in {countdown}s</div>
            <div className="t">{episodeLabel(upNext)} · {upNext.title}</div>
            <div className="row-btns">
              <button className="btn primary sm" onClick={() => nav(`/play/${upNext.id}`, { replace: true })}><Play size={16} /> Play now</button>
              <button className="btn glass sm" onClick={() => { setUpNext(null); goBack(); }}>Cancel</button>
            </div>
          </div>
        )}
        {!upNext && <button className="btn glass" onClick={goBack}>Back</button>}
      </div>
    );
  }

  const showVideo = info && !unsupported && (!hlsPlan || !!session);

  return (
    <div ref={wrap} className={`player ${idle && playing ? "idle" : ""}`}>
      {showVideo && (
        <video
          key={session?.id || "direct"}
          ref={video}
          src={session ? undefined : info.streamUrl}
          preload="auto"
          playsInline
          onClick={onVideoClick}
          onDoubleClick={fullscreen}
          onLoadedMetadata={onLoaded}
          onPlay={() => setPlaying(true)}
          onPause={() => {
            setPlaying(false);
            report(true);
          }}
          onWaiting={() => setWaiting(true)}
          onPlaying={() => setWaiting(false)}
          onCanPlay={() => setWaiting(false)}
          onTimeUpdate={(e) => {
            const v = e.currentTarget;
            setTime(offset + v.currentTime);
            if (v.buffered.length) setBuffered(offset + v.buffered.end(v.buffered.length - 1));
            report();
          }}
          onDurationChange={(e) => {
            if (!inHls || session?.vod) setDur(e.currentTarget.duration);
          }}
          onEnded={onEnded}
          onError={() => {
            const code = video.current?.error?.code;
            if (inHls) setUnsupported("The server couldn't convert this file for the browser");
            else setUnsupported(code === 4 ? "This browser can't play this file's format" : "Couldn't load the file from storage");
          }}
        />
      )}

      {(!info || waiting) && !unsupported && (
        <div className="player-spinner">
          <div className="spinner" />
          {preparing && <div className="muted small">Preparing the stream, reading the file from storage…</div>}
        </div>
      )}

      <div className="player-top">
        <button className="icon-btn" onClick={goBack} aria-label="Back"><Back /></button>
        <div>
          <div className="pt">{title}</div>
          <div className="ps">{subtitle}</div>
        </div>
      </div>

      {audioWarn && !unsupported && (
        <div className="player-warn">
          {audioWarn}. Video will play silently here; for sound, open it in an external player.
          {info && <ExternalPlayers info={info} />}
        </div>
      )}

      {unsupported && info && (
        <div className="player-unsupported">
          <h2>Direct play isn't possible in this browser</h2>
          <p>{unsupported}. Open it in a player that supports it. The link streams the original file.</p>
          <ExternalPlayers info={info} />
          {item && <Link to={`/item/${item.id}`} className="muted small">Back to details</Link>}
        </div>
      )}

      {activeMarker && !upNext && !unsupported && (activeMarker.type === "intro" || item?.kind === "episode") && (
        <button className="btn glass skip-btn" onClick={skip}>
          {activeMarker.type === "intro" ? "Skip intro" : "Next episode"}
        </button>
      )}

      {upNext && (
        <div className="up-next">
          <div className="muted small">Up next in {countdown}s</div>
          <div className="t">{episodeLabel(upNext)} · {upNext.title}</div>
          <div className="row-btns">
            <button className="btn primary sm" onClick={() => nav(`/play/${upNext.id}`, { replace: true })}><Play size={16} /> Play now</button>
            <button className="btn glass sm" onClick={() => setUpNext(null)}>Cancel</button>
          </div>
        </div>
      )}

      {info && !unsupported && (
        <div className="player-controls">
          <div className="scrub">
            <input type="range" min={0} max={dur || 0} step={0.1} value={time}
              onChange={(e) => seekTo(Number(e.target.value))}
              style={{ "--p": `${dur ? (time / dur) * 100 : 0}%`, "--b": `${dur ? (buffered / dur) * 100 : 0}%` } as React.CSSProperties}
              aria-label="Seek" />
          </div>
          <div className="ctrl-row">
            <button className="icon-btn" onClick={toggle} aria-label={playing ? "Pause" : "Play"}>{playing ? <Pause size={24} /> : <Play size={24} />}</button>
            <button className="icon-btn" onClick={() => seek(-10)} aria-label="Back 10 seconds"><Rewind size={22} /></button>
            <button className="icon-btn" onClick={() => seek(10)} aria-label="Forward 10 seconds"><Forward size={22} /></button>
            <div className="vol">
              <button className="icon-btn" onClick={() => setVol(muted || volume === 0 ? 1 : 0)} aria-label="Mute">
                {muted || volume === 0 ? <Mute /> : <Volume />}
              </button>
              <input type="range" min={0} max={1} step={0.05} value={muted ? 0 : volume} onChange={(e) => setVol(Number(e.target.value))}
                style={{ "--p": `${(muted ? 0 : volume) * 100}%` } as React.CSSProperties} aria-label="Volume" />
            </div>
            <div className="time">{fmtTime(time)} <span className="muted">/ {fmtTime(dur)}</span></div>
            <div className="spacer" />
            {inHls && <span className="ends-at muted">Converted for this browser</span>}
            {endsAt && <span className="ends-at">Ends at {endsAt.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}</span>}
            <button className="icon-btn" onClick={fullscreen} aria-label="Fullscreen"><Fullscreen /></button>
          </div>
        </div>
      )}
    </div>
  );
}
