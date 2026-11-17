import { memo } from "react";
import "./rt.css";

/**
 * Rotten Tomatoes scores (copied from Plex): the critics' Tomatometer and
 * the audience score, 0–100. The icons are our own drawings in the familiar
 * style: a fresh tomato or a green splat for critics, an upright or tipped
 * popcorn bucket for the audience. 60% or more counts as fresh/upright.
 */

export interface HasRT {
  rtCritic?: number;
  rtAudience?: number;
}

export const hasRT = (c: HasRT) => c.rtCritic != null || c.rtAudience != null;

/** Fresh tomato: a glossy red fruit with a green leafy top. */
export function Tomato({ size = "1.2em" }: { size?: number | string }) {
  return (
    <svg className="rt-icon" width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      <path d="M12 6.2c5.3 0 9.2 3.4 9.2 8 0 4.5-4 7.8-9.2 7.8S2.8 18.7 2.8 14.2c0-4.6 3.9-8 9.2-8z" fill="#ec3b23" />
      <ellipse cx="8.2" cy="11.6" rx="2.4" ry="1.5" transform="rotate(-28 8.2 11.6)" fill="#ff8a6e" opacity=".75" />
      <path d="M12 8.4c-.9-1.5-2.6-2.4-4.6-2.1 1.2-.4 2.4-.3 3.4.2-.6-1.2-1.7-2-3-2.4 1.6-.1 3 .6 3.9 1.7.2-1.3.9-2.5 2-3.3-.4 1.1-.5 2.2-.2 3.3 1-1 2.4-1.4 3.9-1.1-1.3.4-2.4 1.3-3 2.4 1.2-.4 2.5-.3 3.5.4-1.9-.3-3.6.6-4.6 2.1z"
        fill="#4fa83a" />
    </svg>
  );
}

/** Rotten: a flat green splat with a couple of flecks. */
export function Splat({ size = "1.2em" }: { size?: number | string }) {
  return (
    <svg className="rt-icon" width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      <path d="M11.6 3.6c1 .1 1.3 1.6 2.1 2.1.9.5 2.3-.6 3.1 0 .8.7-.2 2.1.3 2.9.5.8 2.2.6 2.5 1.5.3 1-1.1 1.6-1 2.6.1 1 1.6 1.7 1.2 2.6-.4 1-2 .4-2.7 1.1-.7.7.1 2.3-.8 2.9-.9.6-1.9-.8-2.9-.6-1 .2-1.4 1.9-2.5 1.8-1-.1-1-1.8-1.9-2.3-.9-.5-2.4.5-3.1-.2-.7-.8.4-2-.1-2.9-.4-.8-2.2-.8-2.4-1.8-.2-.9 1.3-1.4 1.3-2.4 0-1-1.4-1.8-1-2.7.5-.9 2 0 2.8-.6.8-.6.4-2.3 1.3-2.8.9-.4 1.8.9 2.8.8z"
        fill="#7ac142" />
      <circle cx="4" cy="5" r="1.1" fill="#7ac142" />
      <circle cx="20.4" cy="19.6" r="1" fill="#7ac142" />
    </svg>
  );
}

/** Audience liked it: a full striped popcorn bucket, standing up. */
export function Popcorn({ size = "1.2em" }: { size?: number | string }) {
  return (
    <svg className="rt-icon" width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      <g fill="#fff4cf">
        <circle cx="7.2" cy="8" r="2.4" /><circle cx="10.6" cy="6.2" r="2.6" /><circle cx="14.2" cy="6.4" r="2.5" />
        <circle cx="17" cy="8.3" r="2.3" /><circle cx="12.3" cy="8.6" r="2.2" />
      </g>
      <path d="M5 9.4h14l-1.7 12H6.7z" fill="#fff" />
      <path d="M7.6 9.4h2.2l.2 12H8.3zM12.1 9.4h2.2l-.5 12h-1.6zM16.6 9.4h2.4l-.4 3-1.1 9h-1.1z" fill="#ec3b23" />
    </svg>
  );
}

/** Audience didn't: the bucket tipped over, kernels spilling out. */
export function Spilled({ size = "1.2em" }: { size?: number | string }) {
  return (
    <svg className="rt-icon" width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      <g transform="translate(-3.2 -1.2) rotate(72 12 15)">
        <path d="M5 9.4h14l-1.7 12H6.7z" fill="#e9f3d7" />
        <path d="M7.6 9.4h2.2l.2 12H8.3zM12.1 9.4h2.2l-.5 12h-1.6zM16.6 9.4h2.4l-.4 3-1.1 9h-1.1z" fill="#7ac142" />
      </g>
      <g fill="#e6e0bc"><circle cx="20.6" cy="12.6" r="1.6" /><circle cx="21.4" cy="17.4" r="1.4" /><circle cx="18.4" cy="20.6" r="1.3" /></g>
    </svg>
  );
}

const fresh = (n: number) => n >= 60;

/** Inline scores for title pages and hero banners. */
export function RTScores({ c, size = "1.25em", labels, className }: { c: HasRT; size?: number | string; labels?: boolean; className?: string }) {
  if (!hasRT(c)) return null;
  const crit = c.rtCritic, aud = c.rtAudience;
  return (
    <span className={`rt ${className || ""}`}>
      {crit != null && (
        <span className="rt-s" title={`Rotten Tomatoes critics: ${crit}% (${fresh(crit) ? "fresh" : "rotten"})`}>
          {fresh(crit) ? <Tomato size={size} /> : <Splat size={size} />}
          <b>{crit}%</b>
          {labels && <small>Critics</small>}
        </span>
      )}
      {aud != null && (
        <span className="rt-s" title={`Rotten Tomatoes audience: ${aud}%`}>
          {fresh(aud) ? <Popcorn size={size} /> : <Spilled size={size} />}
          <b>{aud}%</b>
          {labels && <small>Audience</small>}
        </span>
      )}
    </span>
  );
}

/** The small overlay in the corner of a poster. Plain flat colours (no blur
 *  or shadows) so a grid of hundreds stays cheap to draw on a Fire TV. */
export const RTBadge = memo(function RTBadge({ c }: { c: HasRT }) {
  if (!hasRT(c)) return null;
  return <RTScores c={c} size="1.2em" className="rt-badge" />;
});

// ---- on/off for poster overlays (kept per device) ----

const KEY = "plexodus.rtPosters";

export function rtPostersOn() {
  try {
    return localStorage.getItem(KEY) !== "0";
  } catch {
    return true;
  }
}

function applyClass(on: boolean) {
  document.documentElement.classList.toggle("rt-off", !on);
}

export function setRtPosters(on: boolean) {
  try {
    localStorage.setItem(KEY, on ? "1" : "0");
  } catch {
    /* private mode: still applies until reload */
  }
  applyClass(on);
}

if (typeof document !== "undefined") applyClass(rtPostersOn());
