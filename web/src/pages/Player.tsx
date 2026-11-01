import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { api, Card, Detail, episodeLabel, fmtTime, Marker, MediaFile, PlayInfo } from "../api";
import { Back, Copy, External, Forward, Fullscreen, Mute, Pause, Play, Rewind, Volume } from "../components/Icons";
import { native, NativeEnded, nativePlay } from "../native";

// Plexodus never transcodes. We ask the browser whether it can decode the
// file's codecs and, if it can't, offer external players instead.
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
const badContainers = ["avi", "asf", "flv", "mpeg", "mpegts"];

// WebKit (Safari, and every browser on iOS) has no Matroska demuxer, so an
// .mkv can never play there regardless of its codecs. Deciding this from the
// file name avoids probing the file and waiting for the <video> element to
// fail, which on cold network storage can take minutes.
const webkitOnly = /AppleWebKit/.test(navigator.userAgent) && !/Chrome|Chromium|Edg\//.test(navigator.userAgent);
const neverPlays: Record<string, string> = {
  mkv: "MKV files can't play in Safari",
  avi: "AVI files can't play in a browser",
  wmv: "WMV files can't play in a browser",
  flv: "FLV files can't play in a browser",
  ts: "MPEG-TS files can't play in a browser",
  m2ts: "MPEG-TS files can't play in a browser",
  mpg: "MPEG files can't play in a browser",
  mpeg: "MPEG files can't play in a browser",
};
function unplayableByName(name: string): string | undefined {
  const ext = name.toLowerCase().split(".").pop() || "";
  if (ext === "mkv") return webkitOnly ? neverPlays.mkv : undefined;
  return neverPlays[ext];
}

function compat(f: MediaFile): { video: boolean; audio: boolean; reason?: string } {
  const probe = document.createElement("video");
  const can = (t?: string) => !!t && probe.canPlayType(t) !== "";
  if (!f.probed) return { video: true, audio: true };
  const container = (f.container || "").split(",")[0];
  if (badContainers.includes(container)) return { video: false, audio: false, reason: `${container.toUpperCase()} files can't play in a browser` };
  const v = !f.videoCodec || can(videoCodecs[f.videoCodec]);
  const a = !f.audioCodec || can(audioCodecs[f.audioCodec]) || f.audioCodec.startsWith("pcm");
  let reason: string | undefined;
  if (!v) reason = `This browser can't decode ${f.videoCodec?.toUpperCase()} video`;
  else if (!a) reason = `This browser can't decode ${f.audioCodec?.toUpperCase()} audio`;
  return { video: v, audio: a, reason };
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

  // Load item + play info.
  useEffect(() => {
    let cancelled = false;
    setInfo(undefined);
    setError(undefined);
    setUnsupported(undefined);
    setAudioWarn(undefined);
    setUpNext(null);
    setWaiting(true);
    (async () => {
      try {
        const d = await api.get<Detail>(`/api/items/${id}`);
        if (cancelled) return;
        setItem(d);
        setMarkers([]);
        api.get<Marker[]>(`/api/items/${d.id}/markers`).then((m) => !cancelled && setMarkers(m), () => {});
        if (!d.files.length) throw new Error("No media file for this item.");
        const byName = native ? undefined : unplayableByName(d.files[0].name || "");
        const p = await api.get<PlayInfo>(`/api/play/${d.files[0].id}${byName ? "?probe=0" : ""}`);
        if (cancelled) return;
        if (byName) {
          setInfo(p);
          setUnsupported(byName);
          return;
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
        const c = compat(p.file);
        if (!c.video) setUnsupported(c.reason);
        else if (!c.audio) setAudioWarn(c.reason);
        setInfo(p);
      } catch (e) {
        if (!cancelled) setError((e as Error).message);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [id]);

  const report = useCallback(
    (force = false) => {
      const v = video.current;
      if (!v || !item || !v.duration || !isFinite(v.duration)) return;
      const now = Date.now();
      if (!force && now - lastReport.current < 10000) return;
      lastReport.current = now;
      const body = JSON.stringify({ position: v.currentTime, duration: v.duration });
      if (force && navigator.sendBeacon) {
        navigator.sendBeacon(`/api/items/${item.id}/progress`, new Blob([body], { type: "application/json" }));
      } else {
        fetch(`/api/items/${item.id}/progress`, { method: "POST", body, headers: { "Content-Type": "application/json" } });
      }
    },
    [item],
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
    setDur(v.duration);
    const from = params.get("from");
    const start = from != null ? Number(from) : info?.position || 0;
    if (start > 0 && start < v.duration - 5) v.currentTime = start;
    v.volume = volume;
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
  useEffect(() => {
    let t: ReturnType<typeof setTimeout>;
    const wake = () => {
      setIdle(false);
      clearTimeout(t);
      t = setTimeout(() => setIdle(true), 3000);
    };
    wake();
    window.addEventListener("mousemove", wake);
    window.addEventListener("touchstart", wake);
    window.addEventListener("keydown", wake);
    return () => {
      clearTimeout(t);
      window.removeEventListener("mousemove", wake);
      window.removeEventListener("touchstart", wake);
      window.removeEventListener("keydown", wake);
    };
  }, []);

  const toggle = () => {
    const v = video.current;
    if (!v) return;
    if (v.paused) v.play();
    else v.pause();
  };
  const seek = (delta: number) => {
    const v = video.current;
    if (v) v.currentTime = Math.max(0, Math.min(v.duration || 0, v.currentTime + delta));
  };
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
          if (!document.fullscreenElement) nav(-1);
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
    } else if (video.current) {
      video.current.currentTime = activeMarker.end;
    }
  };
  const endsAt = dur ? new Date(Date.now() + ((dur - time) * 1000) / (video.current?.playbackRate || 1)) : null;

  const title = item?.kind === "episode" ? item.showTitle || item.show?.title : item?.title;
  const subtitle = item?.kind === "episode" ? `${episodeLabel(item)} · ${item.title}` : item?.year ? String(item.year) : "";

  if (error) {
    return (
      <div className="player-msg">
        <p>{error}</p>
        <button className="btn glass" onClick={() => nav(-1)}>Go back</button>
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
              <button className="btn glass sm" onClick={() => { setUpNext(null); nav(-1); }}>Cancel</button>
            </div>
          </div>
        )}
        {!upNext && <button className="btn glass" onClick={() => nav(-1)}>Back</button>}
      </div>
    );
  }

  return (
    <div ref={wrap} className={`player ${idle && playing ? "idle" : ""}`}>
      {info && !unsupported && (
        <video
          ref={video}
          src={info.streamUrl}
          preload="auto"
          playsInline
          onClick={toggle}
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
            setTime(v.currentTime);
            if (v.buffered.length) setBuffered(v.buffered.end(v.buffered.length - 1));
            report();
          }}
          onDurationChange={(e) => setDur(e.currentTarget.duration)}
          onEnded={onEnded}
          onError={() => {
            const code = video.current?.error?.code;
            setUnsupported(code === 4 ? "This browser can't play this file's format" : "Couldn't load the file from storage");
          }}
        />
      )}

      {(!info || waiting) && !unsupported && <div className="player-spinner"><div className="spinner" /></div>}

      <div className="player-top">
        <button className="icon-btn" onClick={() => nav(-1)} aria-label="Back"><Back /></button>
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
              onChange={(e) => {
                if (video.current) video.current.currentTime = Number(e.target.value);
                setTime(Number(e.target.value));
              }}
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
            {endsAt && <span className="ends-at">Ends at {endsAt.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}</span>}
            <button className="icon-btn" onClick={fullscreen} aria-label="Fullscreen"><Fullscreen /></button>
          </div>
        </div>
      )}
    </div>
  );
}
