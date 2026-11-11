import { api, User } from "./api";

// Sign in with Plex from a page served by this server. The server makes a
// plex.tv PIN, the person approves it (in a plex.tv window, or by typing the
// short code at plex.tv/link on a TV), and polling signs them in.

export interface PlexPin {
  id: number;
  code: string;
  authUrl: string;
  expiresIn?: number;
}

export const startPlexPin = (strong: boolean) => api.post<PlexPin>("/api/auth/plex/pin", { strong });

/** Polls until the PIN is approved; resolves with the signed-in user. */
export function waitForPlex(pin: PlexPin, signal: { cancelled: boolean }): Promise<User> {
  const deadline = Date.now() + (pin.expiresIn && pin.expiresIn > 0 ? pin.expiresIn * 1000 : 15 * 60_000);
  return new Promise((resolve, reject) => {
    const tick = async () => {
      if (signal.cancelled) return;
      if (Date.now() > deadline) return reject(new Error("The code expired. Start again."));
      try {
        const r = await api.get<User | { pending: true }>(`/api/auth/plex/pin/${pin.id}`);
        if (signal.cancelled) return;
        if ("pending" in r) setTimeout(tick, 2000);
        else resolve(r);
      } catch (e) {
        reject(e);
      }
    };
    setTimeout(tick, 2000);
  });
}
