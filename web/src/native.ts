// Bridge to the Android app. When the UI runs inside the Plexodus app, the
// shell injects `PlexodusNative`, and playback is handed to its built-in
// player, which decodes everything (MKV, HEVC, AC3, DTS…) the browser can't.
export interface NativePlayRequest {
  itemId: number;
  kind: string;
  url: string;
  title: string;
  subtitle: string;
  startSeconds: number;
  durationSeconds: number;
  markers: { type: string; start: number; end: number }[];
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
}

export const native: Bridge | undefined = (window as unknown as { PlexodusNative?: Bridge }).PlexodusNative;

export const nativePlay = (req: NativePlayRequest) => native?.play(JSON.stringify(req));
