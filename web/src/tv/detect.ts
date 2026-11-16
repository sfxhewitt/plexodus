/** True when the TV interface should be used. `?tv=1` / `?tv=0` force it (remembered). */
export function isTvMode(): boolean {
  const q = new URLSearchParams(window.location.search).get("tv");
  try {
    // "Use the phone-style interface" in TV settings lasts until the app restarts.
    if (!q && sessionStorage.getItem("plexodus.tv.session") === "0") return false;
    if (q === "1" || q === "0") localStorage.setItem("plexodus.tv", q);
    const saved = localStorage.getItem("plexodus.tv");
    if (saved) return saved === "1";
  } catch {
    if (q) return q === "1";
  }
  return isTvDevice();
}

/** True on a TV box (inside the Android app), whatever interface was picked. */
export function isTvDevice(): boolean {
  const n = (window as unknown as { PlexodusNative?: { isTv?: () => boolean } }).PlexodusNative;
  if (!n) return false;
  try {
    if (typeof n.isTv === "function") return !!n.isTv();
  } catch {
    /* older app */
  }
  // Older app builds have no isTv(); recognise TV boxes by their user agent.
  return /\bAFT|Android TV|SHIELD|BRAVIA|GoogleTV|AndroidTV/i.test(navigator.userAgent);
}

/** True when the phone-style interface is showing on a TV, so it can offer the way back. */
export function canReturnToTv(): boolean {
  try {
    if (sessionStorage.getItem("plexodus.tv.session") === "0") return true;
    return localStorage.getItem("plexodus.tv") === "0" && isTvDevice();
  } catch {
    return false;
  }
}

/** Leaves the phone-style interface and reloads into the TV one. */
export function backToTv(): void {
  try {
    sessionStorage.removeItem("plexodus.tv.session");
    if (localStorage.getItem("plexodus.tv") === "0") localStorage.removeItem("plexodus.tv");
  } catch {
    /* ignore */
  }
  window.location.href = "/";
}
