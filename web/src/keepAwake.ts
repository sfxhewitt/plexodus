/**
 * Keeps a phone's screen on while a page is open, using the Screen Wake
 * Lock API. Browsers only allow it on secure (https) addresses. On plain
 * http there is no reliable way on an iPhone: the old silent-video trick
 * (NoSleep.js) no longer keeps iOS awake and puts a media player on the lock
 * screen, so it isn't used. Instead Scene notes offers a secure link (the
 * server's built-in tunnel, internal/api/tunnel.go).
 */
type Lock = { release: () => Promise<void> };
type WakeNav = Navigator & { wakeLock?: { request: (t: "screen") => Promise<Lock> } };

/** True when this page can hold the screen on. */
export function canKeepAwake() {
  return window.isSecureContext && !!(navigator as WakeNav).wakeLock;
}

export class KeepAwake {
  private lock: Lock | null = null;
  on = false;

  /** Resolves true if the screen should now stay on. */
  async enable(): Promise<boolean> {
    this.on = true;
    if (!canKeepAwake()) return false;
    try {
      this.lock = await (navigator as WakeNav).wakeLock!.request("screen");
      return true;
    } catch {
      return false;
    }
  }

  /** Re-take after the page comes back (the lock is dropped when hidden). */
  resume() {
    if (this.on && !document.hidden) this.enable().catch(() => {});
  }

  disable() {
    this.on = false;
    this.lock?.release().catch(() => {});
    this.lock = null;
  }
}
