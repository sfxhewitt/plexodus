// The phone and web library's Filters sheet: watched, mood, feel, content and
// the rest of what the server filters on (internal/api/libfilter.go), with a
// live count. Choices are a draft until "Show" is pressed.
import { useEffect, useState } from "react";
import { api } from "../api";
import { CONTENT, contentLabel, FEEL, feelString, feelWord, GenrePrefs, MOODS, moodLabel, parseFeel, picks, PickState, toggleIn } from "../picks";
import { GenreChips, Sheet } from "./Picks";

export const FILTER_KEYS = ["mood", "content", "feel", "filter", "genre", "added", "decade", "rating", "rt", "res", "length", "age", "yfrom", "yto"] as const;
const MULTI = new Set(["content", "feel"]);

type FacetOpt = { v: string; l: string; count: number };
type Facets = { total: number; facets: { key: string; label: string; multi?: boolean; options: FacetOpt[] }[]; genres: FacetOpt[] };

export function filterQuery(params: URLSearchParams) {
  const q = new URLSearchParams();
  for (const k of FILTER_KEYS) {
    const v = params.get(k);
    if (v) q.set(k, v);
  }
  return q.toString();
}

const LABELS: Record<string, Record<string, string>> = {
  filter: { unwatched: "Unwatched", inprogress: "In progress", watched: "Watched" },
  added: { "7": "Added this week", "30": "New this month", "90": "Added in 3 months" },
  rating: { "8": "Rated 8+", "7": "Rated 7+", "6": "Rated 6+", "5": "Rated 5+" },
  rt: { "90": "Critics 90%+", fresh: "Fresh", rotten: "Rotten", audience: "Audience 80%+" },
  res: { "4k": "4K", "1080": "1080p", "720": "720p", sd: "SD" },
  length: { short: "Under 90 min", medium: "90 min – 2 h", long: "Over 2 h" },
  age: { kids: "Kids", "13": "Up to 13", "16": "Up to 16", adult: "Adults" },
};
function filterLabel(k: string, v: string) {
  if (k === "genre") return v;
  if (k === "mood") return moodLabel(v);
  if (k === "content") return contentLabel(v);
  if (k === "feel") return feelWord(v.split(":")[0], Number(v.split(":")[1]));
  if (k === "yfrom") return `From ${v}`;
  if (k === "yto") return `Up to ${v}`;
  if (k === "decade") return v === "0" ? "Before 1950" : `${v}s`;
  return LABELS[k]?.[v] || v;
}

/** Every filter that's on, one per pill; `without` is the value that removes it. */
export function activeFilters(params: URLSearchParams, skip: string[] = []) {
  const out: { k: string; v: string; label: string; without: string }[] = [];
  for (const k of FILTER_KEYS) {
    const raw = params.get(k);
    if (!raw || skip.includes(k)) continue;
    if (!MULTI.has(k)) out.push({ k, v: raw, label: filterLabel(k, raw), without: "" });
    else for (const v of raw.split(",").filter(Boolean)) out.push({ k, v, label: filterLabel(k, v), without: toggleIn(raw, v) });
  }
  return out;
}

export function FilterSheet({ libId, movies, params, prefs, onPrefs, onApply, onClose }: {
  libId: string; movies: boolean; params: URLSearchParams; prefs: GenrePrefs;
  onPrefs: (g: GenrePrefs) => void; onApply: (draft: URLSearchParams) => void; onClose: () => void;
}) {
  const [draft, setDraft] = useState(() => new URLSearchParams(params));
  const fq = filterQuery(draft);
  const prefKey = prefs.exclude.join() + "|" + prefs.require.join();
  const [facets, setFacets] = useState<Facets>();
  // Sliders fire a lot: count once they settle.
  useEffect(() => {
    let alive = true;
    const t = setTimeout(() => {
      api.invalidate(`/api/libraries/${libId}/facets`);
      api.get<Facets>(`/api/libraries/${libId}/facets?${fq}`).then((f) => alive && setFacets(f), () => {});
    }, 220);
    return () => {
      alive = false;
      clearTimeout(t);
    };
  }, [libId, fq, prefKey]);
  const [mem, setMem] = useState<PickState>();
  const [note, setNote] = useState("");
  useEffect(() => {
    if (movies) picks.state().then(setMem, () => {});
  }, [movies]);

  const put = (k: string, v: string) => setDraft((d) => {
    const n = new URLSearchParams(d);
    if (v) n.set(k, v);
    else n.delete(k);
    return n;
  });
  const feel = parseFeel(draft.get("feel"));
  const setFeel = (k: string, v?: number) => {
    const f = { ...feel };
    if (v === undefined) delete f[k];
    else f[k] = v;
    put("feel", feelString(f));
  };
  const count = (key: string, v: string) => (key === "genre" ? facets?.genres : facets?.facets.find((f) => f.key === key)?.options)?.find((o) => o.v === v)?.count;
  const chips = (key: string, opts: { v: string; l: string }[], multi = false) => (
    <div className="pk-chips">
      {opts.map((o) => {
        const cur = draft.get(key) || "";
        const on = multi ? cur.split(",").includes(o.v) : cur === o.v;
        const n = count(key, o.v);
        return (
          <button key={o.v} className={`pk-chip ${on ? "on" : ""}`} aria-pressed={on}
            onClick={() => put(key, multi ? toggleIn(cur, o.v) : on ? "" : o.v)}>
            {o.l}{n !== undefined && <small>{n.toLocaleString()}</small>}
          </button>
        );
      })}
    </div>
  );
  const fromFacet = (key: string) => (facets?.facets.find((f) => f.key === key)?.options || Object.entries(LABELS[key] || {}).map(([v, l]) => ({ v, l }))).map((o) => ({ v: o.v, l: o.l }));
  const group = (key: string, title: string, multi = false) => {
    const opts = fromFacet(key);
    return opts.length ? <div className="pk-fgroup" key={key}><h3>{title}</h3>{chips(key, opts, multi)}</div> : null;
  };
  const reset = () => setDraft((d) => {
    const n = new URLSearchParams(d);
    FILTER_KEYS.forEach((k) => n.delete(k));
    return n;
  });
  const forget = (what: "rules" | "taste", done: string) => picks.reset(what).then(() => {
    setNote(done);
    picks.state().then(setMem, () => {});
  }, (e: Error) => setNote(e.message));
  const year = (k: "yfrom" | "yto", label: string) => (
    <label>{label} <input type="number" inputMode="numeric" min={1900} max={2100} placeholder="Any" aria-label={`Year ${label.toLowerCase()}`}
      value={draft.get(k) || ""} onChange={(e) => put(k, e.target.value.replace(/\D/g, "").slice(0, 4))} /></label>
  );
  const total = facets?.total;
  const noun = movies ? "film" : "title";

  return (
    <Sheet title="Filters" onClose={onClose} wide foot={<>
      <button className="btn glass" onClick={reset}>Clear all</button>
      <button className="btn primary" onClick={() => onApply(draft)}>
        {total === undefined ? "Show" : total === 0 ? `No ${noun}s match` : `Show ${total.toLocaleString()} ${noun}${total === 1 ? "" : "s"}`}
      </button>
    </>}>
      {group("filter", "Watched")}
      {movies && <div className="pk-fgroup"><h3>Mood</h3>{chips("mood", MOODS.map((m) => ({ v: m.v, l: m.l })))}</div>}
      {movies && (
        <div className="pk-fgroup">
          <h3>Feel <small>Slide to say where a film should sit</small></h3>
          <div className="pk-dims">
            {FEEL.map((d) => {
              const v = feel[d.k];
              return (
                <div key={d.k} className={`pk-dim ${v !== undefined ? "set" : ""}`}>
                  <div className="pk-dim-head"><span>{d.l}</span><output>{v === undefined ? "Any" : feelWord(d.k, v)}</output></div>
                  <input type="range" min={0} max={100} step={5} value={v ?? 50} aria-label={`${d.l}: ${d.lo} to ${d.hi}`}
                    onChange={(e) => setFeel(d.k, Number(e.target.value))} />
                  <div className="pk-dim-ends"><span>{d.lo}</span>
                    {v !== undefined && <button className="pk-link" onClick={() => setFeel(d.k)}>Clear</button>}
                    <span>{d.hi}</span></div>
                </div>
              );
            })}
          </div>
        </div>
      )}
      {movies && <div className="pk-fgroup"><h3>Content</h3>{chips("content", CONTENT, true)}</div>}
      {!!facets?.genres.length && <div className="pk-fgroup"><h3>Genre</h3>{chips("genre", facets.genres.map((g) => ({ v: g.v, l: g.l })))}</div>}
      {movies && group("length", "Length")}
      <div className="pk-fgroup">
        <h3>Year</h3>
        <div className="pk-years">{year("yfrom", "From")}{year("yto", "To")}</div>
        {chips("decade", fromFacet("decade"))}
      </div>
      {group("rating", "Rating")}
      {group("rt", "Rotten Tomatoes")}
      {group("added", "Recently added")}
      {movies && group("res", "Quality")}
      {group("age", "Age rating")}
      {!!(facets?.genres.length || prefs.exclude.length || prefs.require.length) && facets && (
        <div className="pk-fgroup">
          <h3>Never show / must have <small>Press once to rule a genre out, twice to insist on it. Saved for every library.</small></h3>
          <GenreChips genres={[...new Set([...facets.genres.map((g) => g.v), ...prefs.exclude, ...prefs.require])].sort()} prefs={prefs} onChange={onPrefs} />
        </div>
      )}
      {movies && (
        <div className="pk-fgroup">
          <h3>Suggestions <small>{mem ? `${mem.resting.toLocaleString()} resting · ${mem.hidden.toLocaleString()} hidden · ${mem.votes.toLocaleString()} liked or disliked` : ""}</small></h3>
          <div className="pk-chips">
            <button className="pk-chip" onClick={() => forget("rules", "Rested and hidden films can be suggested again")}>Bring back rested &amp; hidden films</button>
            <button className="pk-chip" onClick={() => forget("taste", "Likes and dislikes forgotten")}>Forget my likes &amp; dislikes</button>
            <a className="pk-chip" href="/api/picks/export" download="plexodus-picks.json">Export</a>
          </div>
          {note && <div className="muted pk-meta" role="status">{note}</div>}
        </div>
      )}
    </Sheet>
  );
}
