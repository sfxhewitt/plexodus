// Bridge to the Android app. When the UI runs inside the Plexodus app, the
// shell injects `PlexodusNative`, and playback is handed to its built-in
// player, which decodes everything (MKV, HEVC, AC3, DTS…) the browser can't.
export interface NativePlayRequest {
  itemId: number;
  kind: string;
  url: string;
  title: string;
  subtitle: string;
  /** One-line synopsis, shown while paused. */
  overview?: string;
  startSeconds: number;
  durationSeconds: number;
  markers: { type: string; start: number; end: number }[];
  /** Lets the app's Audio sync control save its fix for this file. */
  fileId?: number;
  /** The file's saved lip sync fix (ms, positive = sound later). */
  audioOffsetMs?: number;
}

export interface NativeEnded {
  itemId: number;
  /** Where the player ended up after playing on to later episodes (newer apps). */
  lastItemId?: number;
  position: number;
  duration: number;
  ended: boolean;
  /** How the sitting's progress reports went (newer apps); passed on to the server's log. */
  stats?: Record<string, unknown>;
  /** The app's message never came; the page noticed the player had closed. Position unknown. */
  missed?: boolean;
}

interface Bridge {
  play(json: string): void;
  changeServer(): void;
  version(): string;
  platform(): string;
  /** Newer apps: embedded frames (trailers) stay inside the page. */
  canEmbed?(): boolean;
  /** Comma-separated player features; missing on older apps. */
  features?(): string;
  /** Apps with "avsync": this device's lip sync offset in ms. */
  audioDelay?(): number;
  setAudioDelay?(ms: number): void;
  /** Apps with "engine": the TV player's engine, "exo" (ExoPlayer, as Plex) or "vlc". */
  playerEngine?(): string;
  setPlayerEngine?(engine: string): void;
  /** Apps with "framerate-option": switch the TV to the film's frame rate (off by default). */
  matchFrameRate?(): boolean;
  setMatchFrameRate?(on: boolean): void;
  /** Opens the flash-and-beep calibration in the app's player. */
  calibrateAudio?(): void;
  /** Apps with "avsync-phone": the player shows a QR code, a phone films the TV and the fix applies itself. */
  calibrateWithPhone?(): void;
  /** Apps with "update": checks GitHub and shows the app's own update dialogs. */
  checkForUpdates?(): void;
}

export const native: Bridge | undefined = (window as unknown as { PlexodusNative?: Bridge }).PlexodusNative;

const nativeFeatures = (): string[] => {
  try {
    return typeof native?.features === "function" ? native.features().split(",") : [];
  } catch {
    return [];
  }
};

export const nativeHas = (feature: string) => nativeFeatures().includes(feature);

/** Inside an Android app that can move its sound for lip sync. */
export const deviceLipSync = () => !!native && nativeHas("avsync");

/** The app can be calibrated by filming it with a phone. */
export const phoneLipSync = () => !!native && nativeHas("avsync-phone") && typeof native.calibrateWithPhone === "function";

export const nativePlay = (req: NativePlayRequest) => {
  // Apps from before "Skip recap" would treat a recap like closing credits
  // and jump to the next episode, so only send recaps to apps that know them.
  const markers = nativeFeatures().includes("recap") ? req.markers : req.markers.filter((m) => m.type !== "recap");
  native?.play(JSON.stringify({ ...req, markers }));
  if (native) playerOpened(req.itemId);
};

// The app tells the page when its player closes ("plexodus:native-ended").
// After a long film that message doesn't always arrive (Fire TV Cube, Nov
// 2026: three films watched to the end kept "Mark watched" on their page
// although the server had them as watched), so the page also notices by
// itself: it is hidden while the player covers it, and when it is back on
// screen, or gets a key press, with no word from the app, it announces the
// close itself. Pages then refetch, and ask the server whether it was finished.
let openPlayer: { itemId: number; at: number; hidden: boolean } | null = null;
let closeTimer: ReturnType<typeof setTimeout> | undefined;

function playerOpened(itemId: number) {
  clearTimeout(closeTimer);
  openPlayer = { itemId, at: Date.now(), hidden: document.hidden };
}

function playerMaybeClosed(input: boolean) {
  const p = openPlayer;
  if (!p) return;
  if (document.hidden) {
    p.hidden = true;
    return;
  }
  // Not covered yet (the player takes a moment to open), unless the page
  // was seen hidden, or is being used again.
  if (!p.hidden && !(input && Date.now() - p.at > 3000)) return;
  clearTimeout(closeTimer);
  // Give the app's own message a moment: it knows where playback stopped.
  closeTimer = setTimeout(() => {
    if (openPlayer !== p || document.hidden) return;
    const detail: NativeEnded = { itemId: p.itemId, lastItemId: p.itemId, position: 0, duration: 0, ended: false, missed: true };
    window.dispatchEvent(new CustomEvent("plexodus:native-ended", { detail }));
  }, 1500);
}

if (native) {
  window.addEventListener("plexodus:native-ended", () => {
    openPlayer = null;
    clearTimeout(closeTimer);
  }, true);
  document.addEventListener("visibilitychange", () => playerMaybeClosed(false));
  window.addEventListener("pageshow", () => playerMaybeClosed(false));
  window.addEventListener("focus", () => playerMaybeClosed(false));
  window.addEventListener("keydown", () => playerMaybeClosed(true), true);
  setInterval(() => playerMaybeClosed(false), 2000);
}
