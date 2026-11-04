// Subtitles in the browser player. Every track is handed to the browser as
// ASS (the server converts SRT and friends) and drawn by libass compiled to
// WebAssembly (JASSUB), so anime typesetting, signs, karaoke and the fonts
// shipped inside the MKV look the way the fansubber made them. Plain
// subtitles get the viewer's size and colour presets on top.
import type JASSUBType from "jassub";

export interface SubTrack {
  id: string;
  codec: string;
  language?: string;
  title?: string;
  format: "ass" | "text" | "image";
  default?: boolean;
  forced?: boolean;
  external?: boolean;
  fileName?: string;
}

export interface SubPrefs {
  // "auto" picks a track for you (forced, default-flagged, or your language
  // when the audio is in another one); "off" keeps subtitles off until you
  // choose one.
  mode: "auto" | "off";
  lang: string;
  size: SizeKey;
  color: ColorKey;
  background: BackgroundKey;
  // For real ASS tracks: keep the file's own styling (recommended for anime)
  // instead of applying the presets.
  keepStyling: boolean;
}

export const sizes = { s: { label: "S", scale: 0.8 }, m: { label: "M", scale: 1 }, l: { label: "L", scale: 1.25 }, xl: { label: "XL", scale: 1.55 } } as const;
export const colors = {
  white: { label: "White", rgb: 0xffffff },
  yellow: { label: "Yellow", rgb: 0xffe14d },
  cyan: { label: "Cyan", rgb: 0x5cf2ff },
  pink: { label: "Pink", rgb: 0xff7ad9 },
} as const;
export const backgrounds = { outline: "Outline", shadow: "Shadow", box: "Box" } as const;
export type SizeKey = keyof typeof sizes;
export type ColorKey = keyof typeof colors;
export type BackgroundKey = keyof typeof backgrounds;

const prefsKey = "plexodus.subs";
const delayKey = (fileId: number) => `plexodus.subdelay.${fileId}`;

function defaultLang() {
  return (navigator.language || "en").split("-")[0].toLowerCase();
}

export function loadPrefs(): SubPrefs {
  const d: SubPrefs = { mode: "auto", lang: defaultLang(), size: "m", color: "white", background: "outline", keepStyling: true };
  try {
    const p = JSON.parse(localStorage.getItem(prefsKey) || "{}");
    return {
      ...d, ...p,
      size: p.size in sizes ? p.size : d.size,
      color: p.color in colors ? p.color : d.color,
      background: p.background in backgrounds ? p.background : d.background,
    };
  } catch {
    return d;
  }
}

export function savePrefs(p: SubPrefs) {
  try {
    localStorage.setItem(prefsKey, JSON.stringify(p));
  } catch {
    /* private mode */
  }
}

export function loadDelay(fileId: number) {
  try {
    return Number(localStorage.getItem(delayKey(fileId))) || 0;
  } catch {
    return 0;
  }
}

export function saveDelay(fileId: number, delay: number) {
  try {
    if (delay) localStorage.setItem(delayKey(fileId), String(delay));
    else localStorage.removeItem(delayKey(fileId));
  } catch {
    /* private mode */
  }
}

// ISO 639-2 (what MKV files use) to 639-1 for the languages anyone is
// likely to have.
const iso3: Record<string, string> = {
  eng: "en", jpn: "ja", spa: "es", fre: "fr", fra: "fr", ger: "de", deu: "de", ita: "it", por: "pt", rus: "ru",
  chi: "zh", zho: "zh", kor: "ko", ara: "ar", dut: "nl", nld: "nl", pol: "pl", swe: "sv", nor: "no", dan: "da",
  fin: "fi", tur: "tr", hin: "hi", tha: "th", vie: "vi", ind: "id", may: "ms", msa: "ms", heb: "he", gre: "el",
  ell: "el", cze: "cs", ces: "cs", hun: "hu", rum: "ro", ron: "ro", ukr: "uk", fil: "tl", tgl: "tl",
};

export function langOf(code?: string) {
  if (!code) return "";
  const c = code.toLowerCase().split("-")[0];
  return iso3[c] || c;
}

const names = (() => {
  try {
    return new Intl.DisplayNames([navigator.language || "en"], { type: "language" });
  } catch {
    return undefined;
  }
})();

export function trackLabel(t: SubTrack, all: SubTrack[]) {
  const lang = langOf(t.language);
  let name = "";
  if (lang && lang !== "und") {
    try {
      name = names?.of(lang) || lang;
    } catch {
      name = lang;
    }
  }
  const parts = [name, t.title && t.title !== name ? t.title : ""].filter(Boolean);
  if (!parts.length) parts.push(t.external ? t.fileName || "External" : `Track ${all.indexOf(t) + 1}`);
  return parts.join(" · ");
}

export function trackTags(t: SubTrack) {
  const tags: string[] = [];
  if (t.format === "ass") tags.push("Styled");
  else if (t.format === "text") tags.push(({ subrip: "SRT", srt: "SRT", vtt: "VTT", webvtt: "VTT" } as Record<string, string>)[t.codec] || "Text");
  if (t.forced) tags.push("Forced");
  if (t.external) tags.push("File");
  return tags;
}

// Pick the track to show when playback starts. Embedded tracks in very
// large files are only picked when flagged, because reading them means
// reading the whole file from storage.
export function pickTrack(tracks: SubTrack[], prefs: SubPrefs, audioLang: string | undefined, fileSize: number): SubTrack | undefined {
  if (prefs.mode === "off") return;
  const usable = tracks.filter((t) => t.format !== "image");
  const mine = (t: SubTrack) => langOf(t.language) === prefs.lang;
  const forced = usable.find((t) => t.forced && (mine(t) || !t.language));
  if (forced) return forced;
  const flagged = usable.find((t) => t.default && (mine(t) || !t.language));
  if (flagged) return flagged;
  const audio = langOf(audioLang);
  if (audio && audio !== "und" && audio !== prefs.lang) {
    const big = fileSize > 12 * 1024 ** 3;
    const candidates = usable.filter((t) => mine(t) && (t.external || !big));
    // Prefer full dialogue over a "Signs & Songs" track.
    return candidates.find((t) => !/sign|song/i.test(t.title || "")) || candidates[0];
  }
}

export interface LoadedTrack {
  content: string;
  fonts: string[];
}

// Fetch a track (and the fonts that came with the video). The first fetch
// of an embedded track may take a while: the server reads the file once to
// pull every subtitle track out of it.
export async function fetchTrack(fileId: number, track: SubTrack, signal: AbortSignal): Promise<LoadedTrack> {
  const fontsP = fetch(`/api/subs/${fileId}/fonts`, { credentials: "same-origin", signal })
    .then((r) => (r.ok ? r.json() : { fonts: [] }))
    .then((d: { fonts: { url: string }[] }) => d.fonts.map((f) => new URL(f.url, location.href).href))
    .catch(() => [] as string[]);
  const r = await fetch(`/api/subs/${fileId}/tracks/${track.id}`, { credentials: "same-origin", signal });
  if (!r.ok) {
    const e = await r.json().catch(() => ({}));
    throw new Error((e as { error?: string }).error || `Couldn't load subtitles (${r.status})`);
  }
  const content = await r.text();
  return { content, fonts: await fontsP };
}

function playResY(content: string) {
  const m = /^PlayResY:\s*(\d+)/m.exec(content);
  if (m) return Number(m[1]) || 288;
  const x = /^PlayResX:\s*(\d+)/m.exec(content);
  return x ? Math.round((Number(x[1]) * 3) / 4) : 288;
}

// libass colours are 0xRRGGBBAA with AA as transparency (0 = opaque).
const rgba = (rgb: number, transparency = 0) => ((rgb << 8) | transparency) >>> 0;

// JASSUB's style override is libass's: sizes are in units of a 288-line
// script (libass rescales them to the track's PlayResY) and the font size is
// further multiplied by 0.3 (ass_set_font_scale). Margins are copied as they
// are, so those are scaled here.
function overrideStyle(prefs: SubPrefs, resY: number) {
  const k = sizes[prefs.size].scale;
  const bg = prefs.background;
  const unit = resY / 288;
  return {
    Name: "Default", FontName: "Liberation Sans", FontSize: (288 * 0.058 * k) / 0.3,
    PrimaryColour: rgba(colors[prefs.color].rgb), SecondaryColour: rgba(0xffffff),
    OutlineColour: rgba(0x000000, bg === "box" ? 0x50 : 0), BackColour: rgba(0x000000, bg === "shadow" ? 0x40 : 0x80),
    Bold: 0, Italic: 0, Underline: 0, StrikeOut: 0, ScaleX: 1, ScaleY: 1, Spacing: 0, Angle: 0,
    BorderStyle: bg === "box" ? 3 : 1,
    Outline: (bg === "box" ? 0.8 : bg === "shadow" ? 0.5 : 1.3) * k,
    Shadow: (bg === "shadow" ? 1.2 : 0) * k,
    Alignment: 2, MarginL: Math.round(20 * unit), MarginR: Math.round(20 * unit), MarginV: Math.round(14 * unit), Encoding: 1,
    treat_fontname_as_pattern: 0, Blur: bg === "shadow" ? 0.6 : 0, Justify: 0,
  };
}

let jassubModule: Promise<typeof import("jassub")> | undefined;
let assets: Promise<{ worker: string; wasm: string; modern: string; font: string }> | undefined;

function loadJassub() {
  jassubModule ||= import("jassub");
  assets ||= Promise.all([
    import("jassub/dist/worker/worker.js?worker&url"),
    import("jassub/dist/wasm/jassub-worker.wasm?url"),
    import("jassub/dist/wasm/jassub-worker-modern.wasm?url"),
    import("jassub/dist/default.woff2?url"),
  ]).then(([w, a, m, f]) => ({
    worker: new URL(w.default, location.href).href,
    wasm: new URL(a.default, location.href).href,
    modern: new URL(m.default, location.href).href,
    font: new URL(f.default, location.href).href,
  }));
  return Promise.all([jassubModule, assets]);
}

// SubRenderer draws one track over one <video>.
export class SubRenderer {
  private inst?: JASSUBType;
  private destroyed = false;
  private resY: number;
  private prefs: SubPrefs;
  ready: Promise<void>;

  constructor(
    video: HTMLVideoElement,
    canvas: HTMLCanvasElement,
    private track: SubTrack,
    loaded: LoadedTrack,
    prefs: SubPrefs,
    timeOffset: number,
  ) {
    this.resY = playResY(loaded.content);
    this.prefs = prefs;
    this.ready = loadJassub().then(async ([{ default: JASSUB }, a]) => {
      if (this.destroyed) return;
      const inst = new JASSUB({
        video, canvas, subContent: loaded.content, fonts: loaded.fonts, timeOffset,
        workerUrl: a.worker, wasmUrl: a.wasm, modernWasmUrl: a.modern,
        availableFonts: { "liberation sans": a.font }, defaultFont: "liberation sans",
        queryFonts: false,
        // Heavily typeset anime can be expensive to draw at 4K; 1080p
        // looks the same on any screen people watch these on.
        prescaleHeightLimit: 1080, maxRenderHeight: 1440,
      });
      this.inst = inst;
      await inst.ready;
      if (this.destroyed) return;
      // Apply the latest presets (they may have changed while loading).
      await this.setStyle(this.prefs);
      // A paused video produces no frames to hang a render on; draw now.
      if (video.videoWidth && !inst._lastDemandTime) {
        await inst.manualRender({ mediaTime: video.currentTime, width: video.videoWidth, height: video.videoHeight, expectedDisplayTime: 0 }, true);
      }
    });
  }

  setOffset(timeOffset: number) {
    if (!this.inst) return;
    this.inst.timeOffset = timeOffset;
    if (this.inst._lastDemandTime) this.inst.manualRender(this.inst._lastDemandTime, true).catch(() => {});
  }

  async setStyle(prefs: SubPrefs) {
    this.prefs = prefs;
    const inst = this.inst;
    if (!inst?.renderer) return;
    if (this.track.format === "ass" && prefs.keepStyling) await inst.renderer.disableStyleOverride();
    else await inst.renderer.styleOverride(overrideStyle(prefs, this.resY));
    if (inst._lastDemandTime) await inst.manualRender(inst._lastDemandTime, true).catch(() => {});
  }

  destroy() {
    this.destroyed = true;
    this.inst?.destroy().catch(() => {});
  }
}
