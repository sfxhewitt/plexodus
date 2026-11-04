import { ReactNode } from "react";
import { art, Card, Wrapped } from "../api";
import "./wrapped.css";

// Plexodus Wrapped as a story: one full-screen neon slide at a time with
// progress bars along the top. Shared by the web and TV interfaces, which
// each supply their own input handling (tap/swipe/keys vs the D-pad).
//
// Everything inside is sized in em, so the page sets one font size and the
// slides scale with it. TV-safe CSS only: no flex gap, inset, aspect-ratio
// or filters, and only opacity/transform animations.

export const hours = (s: number) => Math.round(s / 3600);
const fmtHours = (s: number) => {
  const h = s / 3600;
  return h >= 10 ? `${Math.round(h).toLocaleString()}h` : h >= 1 ? `${h.toFixed(1)}h` : `${Math.round(s / 60)}m`;
};

function day(date?: string, withYear = false) {
  if (!date) return "";
  const [y, m, d] = date.split("-").map(Number);
  return new Date(y, m - 1, d).toLocaleDateString(undefined, { day: "numeric", month: "long", ...(withYear ? { year: "numeric" } : {}) });
}

export function hourLabel(h: number) {
  if (h === 0) return "midnight";
  if (h === 12) return "noon";
  return h < 12 ? `${h}am` : `${h - 12}pm`;
}

const MONTHS = ["J", "F", "M", "A", "M", "J", "J", "A", "S", "O", "N", "D"];

/** Single-series bars: every bar one hue, the peak picked out. */
function Bars({ values, labels, peak, title }: { values: number[]; labels: string[]; peak: number; title: (i: number) => string }) {
  const max = Math.max(1, ...values);
  return (
    <div className="wr-bars" role="img" aria-label={values.map((_, i) => title(i)).join(", ")}>
      {values.map((v, i) => (
        <div key={i} className={`wr-bar ${i === peak ? "peak" : ""}`} title={title(i)}>
          <div className="wr-bar-track">
            <div className="wr-bar-fill" style={{ height: `${Math.max(v > 0 ? 3 : 0, (v / max) * 100)}%` }} />
          </div>
          <span className="wr-bar-label">{labels[i]}</span>
        </div>
      ))}
    </div>
  );
}

function Poster({ c }: { c: Card }) {
  const src = art(c.poster || c.showPoster, "w185");
  return <div className="wr-poster" style={src ? { backgroundImage: `url(${src})` } : undefined}>{!src && c.title.slice(0, 1)}</div>;
}

export interface Slide {
  key: string;
  /** Artwork shown behind the slide, faded into the neon background. */
  backdrop?: string;
  body: ReactNode;
}

function TopList({ items, unit }: { items: Wrapped["topShows"]; unit: (n: number) => string }) {
  return (
    <ol className="wr-list">
      {items.map((t, i) => (
        <li key={t.card.id} className="wr-in" style={{ animationDelay: `${0.15 + i * 0.12}s` }}>
          <span className="wr-rank">{i + 1}</span>
          <Poster c={t.card} />
          <div className="wr-list-text">
            <div className="wr-list-title">{t.card.title}</div>
            <div className="wr-list-sub">{unit(t.plays)} · {fmtHours(t.seconds)}</div>
          </div>
        </li>
      ))}
    </ol>
  );
}

/** Builds the slides for a year. `end` is extra content for the last slide (buttons). */
export function wrappedSlides(w: Wrapped, end?: ReactNode): Slide[] {
  if (!w.plays) {
    return [{
      key: "empty",
      body: (
        <div className="wr-center">
          <div className="wr-eyebrow">Plexodus Wrapped</div>
          <div className="wr-huge wr-neon">{w.year}</div>
          <p className="wr-lead">Nothing watched in {w.year} yet.</p>
          <p className="wr-soft">Everything you finish here, and your Plex play history, fills this in.</p>
          {end}
        </div>
      ),
    }];
  }
  const slides: Slide[] = [];
  const topShow = w.topShows[0]?.card;
  const topMovie = w.topMovies[0]?.card;

  slides.push({
    key: "intro",
    backdrop: (topShow || topMovie)?.backdrop,
    body: (
      <div className="wr-center">
        <div className="wr-eyebrow wr-in">Plexodus Wrapped</div>
        <div className="wr-huge wr-neon wr-pop">{w.year}</div>
        <p className="wr-lead wr-in" style={{ animationDelay: ".35s" }}>Your year in watching.</p>
      </div>
    ),
  });

  slides.push({
    key: "time",
    body: (
      <div className="wr-center">
        <div className="wr-eyebrow wr-in">You spent</div>
        <div className="wr-huge wr-neon wr-pop">{hours(w.seconds).toLocaleString()}</div>
        <div className="wr-big wr-in" style={{ animationDelay: ".2s" }}>hours watching</div>
        <p className="wr-soft wr-in" style={{ animationDelay: ".45s" }}>
          That's {(w.seconds / 86400).toFixed(1)} full days, across {w.days} {w.days === 1 ? "day" : "days"} of the year.
        </p>
        <div className="wr-stats wr-in" style={{ animationDelay: ".7s" }}>
          <div><b>{w.movies.toLocaleString()}</b><span>{w.movies === 1 ? "film" : "films"}</span></div>
          <div><b>{w.episodes.toLocaleString()}</b><span>{w.episodes === 1 ? "episode" : "episodes"}</span></div>
          <div><b>{w.shows.toLocaleString()}</b><span>{w.shows === 1 ? "show" : "shows"}</span></div>
        </div>
      </div>
    ),
  });

  if (w.topShows.length) {
    slides.push({
      key: "shows",
      backdrop: topShow?.backdrop,
      body: (
        <div className="wr-panel">
          <div className="wr-eyebrow wr-in">Your top shows</div>
          <h2 className="wr-title wr-in">{topShow!.title} was your show of the year</h2>
          <TopList items={w.topShows} unit={(n) => `${n} ${n === 1 ? "episode" : "episodes"}`} />
        </div>
      ),
    });
  }

  if (w.topMovies.length) {
    slides.push({
      key: "films",
      backdrop: topMovie?.backdrop,
      body: (
        <div className="wr-panel">
          <div className="wr-eyebrow wr-in">Your top films</div>
          <h2 className="wr-title wr-in">{w.movies.toLocaleString()} {w.movies === 1 ? "film" : "films"}, and these led the way</h2>
          <TopList items={w.topMovies} unit={(n) => (n === 1 ? "watched once" : `watched ${n} times`)} />
        </div>
      ),
    });
  }

  if (w.binge) {
    const b = w.binge;
    slides.push({
      key: "binge",
      backdrop: b.show.backdrop,
      body: (
        <div className="wr-center">
          <div className="wr-eyebrow wr-in">Binge record</div>
          <div className="wr-huge wr-neon wr-pop">{b.episodes}</div>
          <div className="wr-big wr-in" style={{ animationDelay: ".2s" }}>episodes of {b.show.title}</div>
          <p className="wr-soft wr-in" style={{ animationDelay: ".45s" }}>in a single day, on {day(b.date)}. That's {fmtHours(b.seconds)} without leaving the sofa.</p>
        </div>
      ),
    });
  }

  const peakMonth = w.months.indexOf(Math.max(...w.months));
  slides.push({
    key: "streak",
    body: (
      <div className="wr-panel">
        <div className="wr-eyebrow wr-in">{w.streak.days > 1 ? "Longest streak" : "Your year"}</div>
        {w.streak.days > 1 ? (
          <>
            <div className="wr-huge wr-neon wr-pop">{w.streak.days} days</div>
            <p className="wr-soft wr-in">in a row with something on, from {day(w.streak.start)} to {day(w.streak.end)}.</p>
          </>
        ) : (
          <h2 className="wr-title wr-in">Month by month</h2>
        )}
        <div className="wr-in" style={{ animationDelay: ".4s" }}>
          <Bars values={w.months} labels={MONTHS} peak={peakMonth}
            title={(i) => `${new Date(2000, i, 1).toLocaleDateString(undefined, { month: "long" })}: ${fmtHours(w.months[i])}`} />
          <p className="wr-caption">Hours per month · busiest: {new Date(2000, peakMonth, 1).toLocaleDateString(undefined, { month: "long" })}</p>
        </div>
      </div>
    ),
  });

  slides.push({
    key: "clock",
    body: (
      <div className="wr-panel">
        <div className="wr-eyebrow wr-in">Peak time: {hourLabel(w.peakHour)}</div>
        <div className="wr-huge-sm wr-neon wr-pop">{w.persona}</div>
        <p className="wr-soft wr-in">{w.blurb}</p>
        <div className="wr-in" style={{ animationDelay: ".4s" }}>
          <Bars values={w.hours} labels={w.hours.map((_, h) => (h % 6 === 0 ? hourLabel(h).replace("midnight", "12am") : ""))} peak={w.peakHour}
            title={(h) => `${hourLabel(h)}: ${fmtHours(w.hours[h])}`} />
          <p className="wr-caption">Hours watched by time of day</p>
        </div>
      </div>
    ),
  });

  if (w.topGenres.length) {
    const max = w.topGenres[0].seconds || 1;
    slides.push({
      key: "genres",
      body: (
        <div className="wr-panel">
          <div className="wr-eyebrow wr-in">Your genres</div>
          <h2 className="wr-title wr-in">Mostly {w.topGenres[0].name.toLowerCase()}</h2>
          <div className="wr-hbars">
            {w.topGenres.map((g, i) => (
              <div key={g.name} className={`wr-hbar wr-in ${i === 0 ? "peak" : ""}`} style={{ animationDelay: `${0.2 + i * 0.1}s` }}>
                <div className="wr-hbar-name">{g.name}</div>
                <div className="wr-hbar-track"><div className="wr-hbar-fill" style={{ width: `${Math.max(3, (g.seconds / max) * 100)}%` }} /></div>
                <div className="wr-hbar-val">{fmtHours(g.seconds)}</div>
              </div>
            ))}
          </div>
        </div>
      ),
    });
  }

  slides.push({
    key: "summary",
    backdrop: (topShow || topMovie)?.backdrop,
    body: (
      <div className="wr-panel">
        <div className="wr-eyebrow wr-in">That was {w.year}</div>
        <div className="wr-grid wr-in" style={{ animationDelay: ".15s" }}>
          <div><span>Hours</span><b className="wr-neon">{hours(w.seconds).toLocaleString()}</b></div>
          <div><span>You are</span><b>{w.persona.replace(/^The /, "")}</b></div>
          {topShow && <div><span>Top show</span><b>{topShow.title}</b></div>}
          {topMovie && <div><span>Top film</span><b>{topMovie.title}</b></div>}
          <div><span>Longest streak</span><b>{w.streak.days} {w.streak.days === 1 ? "day" : "days"}</b></div>
          {w.topGenres[0] && <div><span>Top genre</span><b>{w.topGenres[0].name}</b></div>}
          {w.biggestDay && <div><span>Biggest day</span><b>{day(w.biggestDay.date)} · {fmtHours(w.biggestDay.seconds)}</b></div>}
          {w.first && <div><span>First of the year</span><b>{w.first.card.title}</b></div>}
        </div>
        {end}
      </div>
    ),
  });
  return slides;
}

/**
 * Renders slide `index` of `slides`. `paused` stops the progress bar; when the
 * active bar fills up, `onDone` fires (the parent moves on).
 */
export function WrappedStory({ slides, index, paused, onDone, className }: {
  slides: Slide[]; index: number; paused?: boolean; onDone?: () => void; className?: string;
}) {
  const s = slides[Math.min(index, slides.length - 1)];
  const last = index >= slides.length - 1;
  return (
    <div className={`wr ${className || ""}`}>
      <div className="wr-bg" aria-hidden="true">
        {s.backdrop && <div key={s.key} className="wr-bg-img" style={{ backgroundImage: `url(${art(s.backdrop, "w1280")})` }} />}
        <div className="wr-bg-glow" />
      </div>
      <div className="wr-progress" aria-hidden="true">
        {slides.map((x, i) => (
          <div key={x.key} className="wr-progress-seg">
            <div
              key={i === index ? `on-${index}` : "off"}
              className={`wr-progress-fill ${i < index ? "done" : i === index ? (last ? "done" : "run") : ""} ${paused ? "paused" : ""}`}
              onAnimationEnd={i === index && !last ? onDone : undefined}
            />
          </div>
        ))}
      </div>
      <div className="wr-slide" key={s.key} aria-live="polite">{s.body}</div>
    </div>
  );
}
