import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import Hls from "hls.js";
import { api, Card, Detail, episodeLabel, fmtTime, Marker, MediaFile, PlayInfo } from "../api";
import { Back, Copy, External, Forward, Fullscreen, Mute, Pause, Play, Rewind, Subtitles, Volume } from "../components/Icons";
import { native, NativeEnded, nativePlay } from "../native";
import {
  backgrounds, BackgroundKey, colors, ColorKey, fetchTrack, langOf, LoadedTrack, loadDelay, loadPrefs, pickTrack, saveDelay, savePrefs,
  sizes, SizeKey, SubPrefs, SubRenderer, SubTrack, trackLabel, trackTags,
} from "../subtitles";

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
// Audio that can ride along unchanged inside fragmented MP4 (HLS). Safari
// on iPhone has stayed silent with audio copied from MKV files, even plain
// AAC, so WebKit always gets a freshly encoded AAC track; it costs almost
// nothing and every Apple device decodes it.
const hlsAudioCopy = new Set(["aac", "mp3", "ac3", "eac3", "flac"]);
const hlsAudioCopyWebKit = new Set<string>();
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

/**
 * The remux plan for a file the browser could also play directly; used to
 * move sound earlier for lip sync, which direct play can't do.
 */
function remuxPlan(f: MediaFile): Extract<Plan, { mode: "hls" }> | undefined {
  const probe = document.createElement("video");
  const can = (t?: string) => !!t && probe.canPlayType(t) !== "";
  if (!f.videoCodec || !hlsVideoCopy.has(f.videoCodec) || !can(videoCodecs[f.videoCodec])) return undefined;
  const audio = (f.streams || []).filter((s) => s.type === "audio");
  const track = audio.find((s) => s.default) || audio[0];
  const codec = track?.codec || f.audioCodec || "";
  const copy = (webkitOnly ? hlsAudioCopyWebKit : hlsAudioCopy).has(codec) && can(audioCodecs[codec]);
  return { mode: "hls", audioMode: copy ? "copy" : "aac", audioIndex: track?.index };
}

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
    const copy = (webkitOnly ? hlsAudioCopyWebKit : hlsAudioCopy).has(codec) && can(audioCodecs[codec]);
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

  // Subtitles (see ../subtitles.ts).
  const [subTracks, setSubTracks] = useState<SubTrack[]>([]);
  const [subTrack, setSubTrack] = useState<SubTrack>();
  const [subLoaded, setSubLoaded] = useState<LoadedTrack & { id: string }>();
  const [subState, setSubState] = useState<{ loading?: boolean; error?: string }>({});
  const [subPrefs, setSubPrefsState] = useState<SubPrefs>(loadPrefs);
  const [subDelay, setSubDelayState] = useState(0);
  const [subMenu, setSubMenu] = useState(false);
  const [toast, setToast] = useState<string>();
  const subLayer = useRef<HTMLDivElement>(null);
  const subRenderer = useRef<SubRenderer>();
  const lastSubTrack = useRef<SubTrack>();

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

  // Lip sync: the file's saved audio offset (ms, positive = sound later).
  // Remuxed streams get it from the server, which shifts the audio track's
  // timestamps; direct play can only delay sound, through Web Audio.
  const [audioOffset, setAudioOffsetState] = useState(0);
  const audioOffsetRef = useRef(0);
  const audioGraph = useRef<{ ctx: AudioContext; el?: HTMLVideoElement; delay?: DelayNode }>();
  const offsetTimer = useRef<ReturnType<typeof setTimeout>>();
  const saveTimer = useRef<ReturnType<typeof setTimeout>>();

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
    const s = await api.post<HlsSession>("/api/hls", {
      fileId, start, audioMode: p.audioMode, audioIndex: p.audioIndex, audioOffsetMs: audioOffsetRef.current,
    });
    setSession(s);
    setTime(s.start);
    setBuffered(s.start);
  }, [stopSession]);

  // Load item + play info.
  useEffect(() => {
    let cancelled = false;
    const markerTimers: number[] = [];
    setInfo(undefined);
    setError(undefined);
    setUnsupported(undefined);
    setAudioWarn(undefined);
    setUpNext(null);
    setWaiting(true);
    setSession(undefined);
    setHlsPlan(undefined);
    setSubTracks([]);
    setSubTrack(undefined);
    setSubLoaded(undefined);
    setSubState({});
    audioOffsetRef.current = 0;
    setAudioOffsetState(0);
    (async () => {
      try {
        const d = await api.get<Detail>(`/api/items/${id}`);
        if (cancelled) return;
        setItem(d);
        setMarkers([]);
        api.get<Marker[]>(`/api/items/${d.id}/markers?recap=1`).then((m) => !cancelled && setMarkers(m), () => {});
        // The server looks for recaps (and intros Plex didn't mark) when
        // playback starts; pick up what it finds in the first minutes.
        for (const after of [15, 40, 90]) {
          markerTimers.push(
            window.setTimeout(() => {
              if (!cancelled) api.get<Marker[]>(`/api/items/${d.id}/markers?recap=1`).then((m) => !cancelled && setMarkers(m), () => {});
            }, after * 1000),
          );
        }
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
          const m = await api.get<Marker[]>(`/api/items/${d.id}/markers?recap=1`).catch(() => [] as Marker[]);
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
            fileId: file.id,
            audioOffsetMs: p.audioOffsetMs || 0,
          });
          return;
        }
        let pl = plan(p.file, file.name || p.fileName, p.hls);
        audioOffsetRef.current = p.audioOffsetMs || 0;
        setAudioOffsetState(audioOffsetRef.current);
        // A saved fix that moves the sound earlier needs the remux, even
        // for a file the browser could play as it is.
        if (pl.mode === "direct" && audioOffsetRef.current < 0 && p.hls) {
          const r = remuxPlan(p.file);
          if (r) pl = r;
        }
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
      markerTimers.forEach(clearTimeout);
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

  // Subtitles: list the tracks once the file is known to play here, and
  // pick one if the viewer's preferences call for it.
  const fileId = info?.file.id;
  const playable = !!info && !unsupported && !nativeMode;
  useEffect(() => {
    if (!playable || !fileId || !info) return;
    let cancelled = false;
    setSubDelayState(loadDelay(fileId));
    api.get<{ tracks: SubTrack[] }>(`/api/subs/${fileId}`).then((d) => {
      if (cancelled) return;
      setSubTracks(d.tracks);
      const audio = (info.file.streams || []).filter((s) => s.type === "audio");
      const a = audio.find((s) => s.default) || audio[0];
      const t = pickTrack(d.tracks, loadPrefs(), a?.language, info.file.size || 0);
      if (t) setSubTrack(t);
    }, () => {});
    return () => {
      cancelled = true;
    };
  }, [playable, fileId]);

  // Fetch the chosen track (the first one from a file can take a while: the
  // server reads the file once for all of its subtitle tracks).
  useEffect(() => {
    setSubLoaded(undefined);
    if (!subTrack || !fileId) {
      setSubState({});
      return;
    }
    lastSubTrack.current = subTrack;
    const ac = new AbortController();
    setSubState({ loading: true });
    fetchTrack(fileId, subTrack, ac.signal).then(
      (l) => {
        setSubLoaded({ ...l, id: subTrack.id });
        setSubState({});
      },
      (e: Error) => {
        if (!ac.signal.aborted) setSubState({ error: e.message });
      },
    );
    return () => ac.abort();
  }, [subTrack, fileId]);

  // Draw the track over the video. Every renderer gets a fresh <canvas>
  // (it takes the canvas over for good), made here rather than by React.
  const subKey = subLoaded && `${session?.id || "direct"}:${subLoaded.id}`;
  useEffect(() => {
    const v = video.current;
    const layer = subLayer.current;
    if (!subKey || !subLoaded || !subTrack || !v || !layer) return;
    const c = document.createElement("canvas");
    c.className = "sub-canvas";
    layer.appendChild(c);
    const r = new SubRenderer(v, c, subTrack, subLoaded, subPrefs, offset - subDelay);
    subRenderer.current = r;
    r.ready.catch((e: Error) => setSubState({ error: `Couldn't start the subtitle renderer: ${e.message}` }));
    return () => {
      r.destroy();
      c.remove();
      if (subRenderer.current === r) subRenderer.current = undefined;
    };
  }, [subKey]);
  useEffect(() => {
    subRenderer.current?.setOffset(offset - subDelay);
  }, [offset, subDelay]);
  useEffect(() => {
    subRenderer.current?.setStyle(subPrefs).catch(() => {});
  }, [subPrefs]);

  const setSubPrefs = (p: Partial<SubPrefs>) => {
    setSubPrefsState((cur) => {
      const next = { ...cur, ...p };
      savePrefs(next);
      return next;
    });
  };
  const chooseSub = (t?: SubTrack) => {
    setSubTrack(t);
    if (t) setSubPrefs({ mode: "auto", ...(t.language ? { lang: langOf(t.language) } : {}) });
    else setSubPrefs({ mode: "off" });
  };
  const flash = useRef<ReturnType<typeof setTimeout>>();
  const showToast = (msg: string) => {
    setToast(msg);
    clearTimeout(flash.current);
    flash.current = setTimeout(() => setToast(undefined), 1500);
  };
  const setSubDelay = (d: number) => {
    d = Math.round(Math.max(-30, Math.min(30, d)) * 10) / 10;
    setSubDelayState(d);
    if (fileId) saveDelay(fileId, d);
  };
  const nudgeSubDelay = (by: number) => {
    const d = Math.round((subDelay + by) * 10) / 10;
    setSubDelay(d);
    showToast(`Subtitle delay ${fmtDelay(d)}`);
  };
  const toggleSubs = () => {
    if (subTrack) {
      chooseSub(undefined);
      showToast("Subtitles off");
    } else {
      const usable = subTracks.filter((t) => t.format !== "image");
      const t = (lastSubTrack.current && usable.find((x) => x.id === lastSubTrack.current!.id)) || pickTrack(usable, { ...subPrefs, mode: "auto" }, "und", 0) || usable[0];
      if (t) {
        chooseSub(t);
        showToast(`Subtitles: ${trackLabel(t, subTracks)}`);
      } else showToast("No subtitles for this video");
    }
  };
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
    if (audioOffsetRef.current > 0) delayDirect(audioOffsetRef.current);
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

  /** Delays the sound of a directly played file (Web Audio can't advance it). */
  const delayDirect = (ms: number) => {
    const v = video.current;
    if (!v) return;
    let g = audioGraph.current;
    if (!g) {
      if (ms <= 0) return;
      g = { ctx: new AudioContext() };
      audioGraph.current = g;
    }
    if (g.el !== v) {
      // A media element can be wired into Web Audio once; a new <video>
      // (after switching streams) gets its own source.
      const src = g.ctx.createMediaElementSource(v);
      const delay = g.ctx.createDelay(5);
      src.connect(delay).connect(g.ctx.destination);
      g.el = v;
      g.delay = delay;
    }
    g.delay!.delayTime.value = Math.max(0, ms) / 1000;
    if (g.ctx.state === "suspended") g.ctx.resume().catch(() => {});
  };

  const applyAudioOffset = (ms: number) => {
    clearTimeout(offsetTimer.current);
    if (inHls) {
      // The server shifts the audio track; restart the stream where we are.
      offsetTimer.current = setTimeout(() => {
        if (!hlsPlan || !item) return;
        report(true);
        startSession(hlsPlan, item.files[0].id, absTime()).catch((e) => setError((e as Error).message));
      }, 700);
      return;
    }
    if (ms >= 0) {
      delayDirect(ms);
      return;
    }
    // Sound earlier than the file has it: only the remux can do that.
    const r = info?.hls ? remuxPlan(info.file) : undefined;
    if (!r || !item) {
      showToast("This browser can only delay the sound for this file");
      setAudioOffset(0, false);
      return;
    }
    delayDirect(0);
    offsetTimer.current = setTimeout(() => {
      report(true);
      setHlsPlan(r);
      startSession(r, item.files[0].id, absTime()).catch((e) => setError((e as Error).message));
    }, 700);
  };

  /** Changes the file's lip sync fix, applies it, and saves it for the file. */
  const setAudioOffset = (ms: number, save = true) => {
    const v = Math.max(-5000, Math.min(5000, Math.round(ms)));
    audioOffsetRef.current = v;
    setAudioOffsetState(v);
    applyAudioOffset(v);
    if (!save || !info) return;
    clearTimeout(saveTimer.current);
    const fileId = info.file.id;
    saveTimer.current = setTimeout(() => {
      api.put(`/api/files/${fileId}/audio-offset`, { offsetMs: v }).catch((e) => showToast(`Couldn't save audio sync: ${(e as Error).message}`));
    }, 800);
  };
  const nudgeAudioOffset = (by: number) => {
    const v = audioOffsetRef.current + by;
    setAudioOffset(v);
    showToast(`Audio sync: ${fmtOffset(v)}`);
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
        case "c":
          toggleSubs();
          break;
        case "z":
          if (subTrack) nudgeSubDelay(-0.1);
          break;
        case "x":
          if (subTrack) nudgeSubDelay(0.1);
          break;
        case "[":
          nudgeAudioOffset(-50);
          break;
        case "]":
          nudgeAudioOffset(50);
          break;
        case "Escape":
          if (subMenu) setSubMenu(false);
          else if (!document.fullscreenElement) goBack();
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
    <div ref={wrap} className={`player ${idle && playing && !subMenu ? "idle" : ""}`}>
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
      {showVideo && <div ref={subLayer} className="sub-layer" />}
      {toast && <div className="player-toast">{toast}</div>}

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

      {activeMarker && !upNext && !unsupported && (activeMarker.type !== "credits" || item?.kind === "episode") && (
        <button className="btn glass skip-btn" onClick={skip}>
          {activeMarker.type === "intro" ? "Skip intro" : activeMarker.type === "recap" ? "Skip recap" : "Next episode"}
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
            <div className="sub-wrap">
              <button className={`icon-btn ${subTrack ? "on" : ""} ${subState.loading ? "busy" : ""}`} onClick={() => setSubMenu((o) => !o)}
                aria-label="Subtitles and audio sync" aria-expanded={subMenu} title="Subtitles and audio sync (c)">
                <Subtitles size={22} />
              </button>
              {subMenu && (
                <SubtitleMenu
                  tracks={subTracks} current={subTrack} state={subState} prefs={subPrefs} delay={subDelay}
                  onPick={chooseSub} onPrefs={setSubPrefs} onDelay={setSubDelay} onClose={() => setSubMenu(false)}
                  audioOffset={audioOffset} onAudioOffset={(ms) => setAudioOffset(ms)}
                />
              )}
            </div>
            <button className="icon-btn" onClick={fullscreen} aria-label="Fullscreen"><Fullscreen /></button>
          </div>
        </div>
      )}
    </div>
  );
}

function fmtOffset(ms: number) {
  if (!ms) return "in sync";
  return `sound ${Math.abs(ms)} ms ${ms > 0 ? "later" : "earlier"}`;
}

function fmtDelay(d: number) {
  if (!d) return "0.0 s";
  return `${d > 0 ? "+" : "−"}${Math.abs(d).toFixed(1)} s`;
}

function SubtitleMenu(props: {
  tracks: SubTrack[];
  current?: SubTrack;
  state: { loading?: boolean; error?: string };
  prefs: SubPrefs;
  delay: number;
  onPick: (t?: SubTrack) => void;
  onPrefs: (p: Partial<SubPrefs>) => void;
  onDelay: (d: number) => void;
  onClose: () => void;
  audioOffset: number;
  onAudioOffset: (ms: number) => void;
}) {
  const { tracks, current, state, prefs, delay, onPick, onPrefs, onDelay, audioOffset, onAudioOffset } = props;
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const onDown = (e: PointerEvent) => {
      const t = e.target as HTMLElement;
      if (!ref.current?.contains(t) && !t.closest(".sub-wrap")) props.onClose();
    };
    window.addEventListener("pointerdown", onDown);
    return () => window.removeEventListener("pointerdown", onDown);
  });
  const presetsApply = !current || current.format !== "ass" || !prefs.keepStyling;
  // Picking a preset while the file's own styling is on means "use mine".
  const preset = (p: Partial<SubPrefs>) => onPrefs(presetsApply ? p : { ...p, keepStyling: false });
  return (
    <div className="sub-menu" ref={ref} role="dialog" aria-label="Subtitles">
      <div className="sub-sec">
        <div className="sub-h">Subtitles</div>
        <button className={`sub-opt ${!current ? "sel" : ""}`} onClick={() => onPick(undefined)}>Off</button>
        {tracks.map((t) => (
          <button key={t.id} className={`sub-opt ${current?.id === t.id ? "sel" : ""}`} disabled={t.format === "image"}
            title={t.format === "image" ? "Picture-based subtitles (PGS/VobSub) can't be shown in the browser" : t.fileName}
            onClick={() => onPick(t)}>
            <span className="sub-name">{trackLabel(t, tracks)}</span>
            <span className="sub-tags">
              {(t.format === "image" ? ["Picture"] : trackTags(t)).map((x) => <span key={x} className="sub-tag">{x}</span>)}
            </span>
          </button>
        ))}
        {!tracks.length && <div className="muted small sub-note">This video has no subtitles.</div>}
        {state.loading && <div className="muted small sub-note"><span className="spinner sm" /> Loading subtitles… the first time, the server reads the whole file once.</div>}
        {state.error && <div className="sub-note sub-err">{state.error}</div>}
      </div>

      <div className="sub-sec">
        <div className="sub-h">Style</div>
        {current?.format === "ass" && (
          <label className="sub-toggle">
            <input type="checkbox" checked={prefs.keepStyling} onChange={(e) => onPrefs({ keepStyling: e.target.checked })} />
            <span>Keep the file's own styling <span className="muted">(best for anime)</span></span>
          </label>
        )}
        <div className={`sub-presets ${presetsApply ? "" : "dim"}`}>
          <div className="sub-row">
            <span className="sub-l">Size</span>
            {(Object.keys(sizes) as SizeKey[]).map((k) => (
              <button key={k} className={`sub-chip ${prefs.size === k ? "sel" : ""}`} onClick={() => preset({ size: k })}>{sizes[k].label}</button>
            ))}
          </div>
          <div className="sub-row">
            <span className="sub-l">Colour</span>
            {(Object.keys(colors) as ColorKey[]).map((k) => (
              <button key={k} className={`swatch ${prefs.color === k ? "sel" : ""}`} aria-label={colors[k].label} title={colors[k].label}
                style={{ background: `#${colors[k].rgb.toString(16).padStart(6, "0")}` }} onClick={() => preset({ color: k })} />
            ))}
          </div>
          <div className="sub-row">
            <span className="sub-l">Edge</span>
            {(Object.keys(backgrounds) as BackgroundKey[]).map((k) => (
              <button key={k} className={`sub-chip ${prefs.background === k ? "sel" : ""}`} onClick={() => preset({ background: k })}>{backgrounds[k]}</button>
            ))}
          </div>
        </div>
      </div>

      <div className="sub-sec">
        <div className="sub-h">
          Timing <span className="sub-delay">{fmtDelay(delay)}{delay ? (delay > 0 ? " later" : " earlier") : ""}</span>
        </div>
        <div className="sub-row">
          <button className="sub-chip" onClick={() => onDelay(delay - 0.1)} aria-label="Subtitles 0.1 seconds earlier">−0.1</button>
          <input type="range" min={-10} max={10} step={0.1} value={Math.max(-10, Math.min(10, delay))} onChange={(e) => onDelay(Number(e.target.value))}
            style={{ "--p": `${((Math.max(-10, Math.min(10, delay)) + 10) / 20) * 100}%` } as React.CSSProperties} aria-label="Subtitle delay" />
          <button className="sub-chip" onClick={() => onDelay(delay + 0.1)} aria-label="Subtitles 0.1 seconds later">+0.1</button>
          <button className="sub-chip" onClick={() => onDelay(0)} disabled={!delay}>Reset</button>
        </div>
        <div className="muted small sub-keys">Keys: c on/off · z earlier · x later</div>
      </div>

      <div className="sub-sec">
        <div className="sub-h">
          Audio sync <span className="sub-delay">{fmtOffset(audioOffset)}</span>
        </div>
        <div className="sub-row">
          <button className="sub-chip" onClick={() => onAudioOffset(audioOffset - 50)} aria-label="Sound 50 ms earlier">−50 ms</button>
          <input type="range" min={-1000} max={1000} step={10} value={Math.max(-1000, Math.min(1000, audioOffset))} onChange={(e) => onAudioOffset(Number(e.target.value))}
            style={{ "--p": `${((Math.max(-1000, Math.min(1000, audioOffset)) + 1000) / 2000) * 100}%` } as React.CSSProperties} aria-label="Audio delay" />
          <button className="sub-chip" onClick={() => onAudioOffset(audioOffset + 50)} aria-label="Sound 50 ms later">+50 ms</button>
          <button className="sub-chip" onClick={() => onAudioOffset(0)} disabled={!audioOffset}>Reset</button>
        </div>
        <div className="muted small sub-keys">For a file whose sound doesn't match the lips; saved for this file on every device. Keys: [ earlier · ] later</div>
      </div>
    </div>
  );
}
