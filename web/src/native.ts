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
  position: number;
  duration: number;
  ended: boolean;
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
  /** Opens the flash-and-beep calibration in the app's player. */
  calibrateAudio?(): void;
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

export const nativePlay = (req: NativePlayRequest) => {
  // Apps from before "Skip recap" would treat a recap like closing credits
  // and jump to the next episode, so only send recaps to apps that know them.
  const markers = nativeFeatures().includes("recap") ? req.markers : req.markers.filter((m) => m.type !== "recap");
  native?.play(JSON.stringify({ ...req, markers }));
};
