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
