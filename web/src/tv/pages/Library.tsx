import { useCallback, useContext, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { api, Card } from "../../api";
import { Check, Compass, Dice, Shuffle, Sliders, SortIcon } from "../../components/Icons";
import { contentLabel, cycleGenre, FEEL, feelString, feelWord, GenrePrefs, genreMode, moodLabel, parseFeel, picks, PickState, toggleIn } from "../../picks";
import { QuizOverlay, SurpriseOverlay } from "../Picks";
import { usePickResume } from "../../picksHooks";
import { BackdropCtx, Spinner, TvButton, TvCard } from "../components";
import { focusInitial, focusKey, onBack } from "../focus";
import { useLibraries, usePageFocus, useRefresh, useToast } from "../hooks";
import { stopTrailer } from "../ambient";
import { playCard } from "../play";

const PAGE = 120;
type Letter = { l: string; offset: number };
type Page = { items: Card[]; total: number; offset: number; letters?: Letter[] };
// The grid holds a window of the list: `items` start at `start`. Jumping to a
// letter starts a new window there, and scrolling up from it loads what's above.
type Grid = { items: Card[]; total: number; start: number; letters?: Letter[] };

// Loaded pages per query, so coming back from a title restores the grid
// (and the focused poster) however far down you'd scrolled.
const loaded = new Map<string, Grid>();

// Sorts as the filter panel lists them, with what each direction means.
const SORTS: { v: string; l: string; asc: string; desc: string; def: "asc" | "desc" }[] = [
  { v: "title", l: "A–Z", asc: "A to Z", desc: "Z to A", def: "asc" },
  { v: "added", l: "Recently added", asc: "Oldest first", desc: "Newest first", def: "desc" },
  { v: "release", l: "Release date", asc: "Oldest first", desc: "Newest first", def: "desc" },
  { v: "rating", l: "Rating", asc: "Lowest first", desc: "Highest first", def: "desc" },
  { v: "critics", l: "Tomatometer", asc: "Lowest first", desc: "Highest first", def: "desc" },
  { v: "audience", l: "Audience score", asc: "Lowest first", desc: "Highest first", def: "desc" },
  { v: "runtime", l: "Length", asc: "Shortest first", desc: "Longest first", def: "asc" },
  { v: "match", l: "Best match", asc: "Closest fit to your mood and taste", desc: "Closest fit to your mood and taste", def: "desc" },
  { v: "random", l: "Shuffle", asc: "Pick again to reshuffle", desc: "Pick again to reshuffle", def: "asc" },
];
const sortInfo = (v: string) => SORTS.find((x) => x.v === v) || (v === "year" ? { ...SORTS[2], v: "year", l: "Year" } : SORTS[0]);

// Every filter the server understands (internal/api/libfilter.go), with labels
// for the chips that show what's on. Counts come from /facets.
const FILTER_KEYS = ["mood", "content", "feel", "filter", "genre", "added", "decade", "rating", "rt", "res", "length", "age", "yfrom", "yto"] as const;
// Filters that hold several choices at once, comma-separated.
const MULTI = new Set(["content", "feel"]);
// The feel sliders on a remote: each press moves a dimension one stop on.
const FEEL_STOPS = [15, 50, 85];
type FilterKey = (typeof FILTER_KEYS)[number];
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

// One-press shortcuts in the bar, like the quick filters on Apple TV and Netflix.
// Few enough to sit on one line beside Surprise me and Help me choose.
const QUICK: { k: FilterKey; v: string; l: string; movies?: boolean }[] = [
  { k: "filter", v: "unwatched", l: "Unwatched" },
  { k: "filter", v: "inprogress", l: "In progress" },
  { k: "added", v: "30", l: "New this month" },
  { k: "rating", v: "7", l: "Highly rated" },
  { k: "rt", v: "fresh", l: "Fresh" },
  { k: "res", v: "4k", l: "4K", movies: true },
];

type FacetOpt = { v: string; l: string; count: number };
type Facets = { total: number; facets: { key: string; label: string; multi?: boolean; options: FacetOpt[] }[]; genres: FacetOpt[] };

/** Every filter that's on, one entry per chip: multi-choice filters give one each. */
function activeFilters(params: URLSearchParams) {
  const out: { k: FilterKey; v: string; label: string; without: string }[] = [];
  for (const k of FILTER_KEYS) {
    const raw = params.get(k);
    if (!raw) continue;
    if (!MULTI.has(k)) out.push({ k, v: raw, label: filterLabel(k, raw), without: "" });
    else for (const v of raw.split(",").filter(Boolean)) out.push({ k, v, label: filterLabel(k, v), without: toggleIn(raw, v) });
  }
  return out;
}

/** The library's filters and sort as they stand in the address. */
function filterQuery(params: URLSearchParams) {
  const q = new URLSearchParams();
  for (const k of FILTER_KEYS) {
    const v = params.get(k);
    if (v) q.set(k, v);
  }
  return q.toString();
}

/**
 * Filter & sort, a panel from the right like Apple TV's and Plex's: the
 * categories down the left, the choices for the focused one on the right with
 * how many titles each would leave. Picking applies at once (the grid updates
 * behind), and picking the chosen one again clears it.
 */
function FilterPanel({ libId, movies, params, startAt, onSet, onClear, onClose, prefs, onPrefs, ver }: {
  libId: string; movies: boolean; params: URLSearchParams; startAt: string;
  onSet: (changes: Record<string, string>, fk: string) => void; onClear: () => void; onClose: (toGrid: boolean) => void;
  prefs: GenrePrefs; onPrefs: (g: GenrePrefs) => void; ver: number;
}) {
  const toast = useToast();
  const fq = filterQuery(params) + (ver ? `&_=${ver}` : "");
  // Every genre in the library, for "Never show" (the counts leave out what's ruled out).
  const [allGenres, setAllGenres] = useState<{ name: string }[]>(() => api.cached<{ name: string }[]>(`/api/libraries/${libId}/genres`) || []);
  const [mem, setMem] = useState<PickState>();
  useEffect(() => {
    api.get<{ name: string }[]>(`/api/libraries/${libId}/genres`).then(setAllGenres, () => {});
    if (movies) picks.state().then(setMem, () => {});
  }, [libId, movies]);
  const feel = parseFeel(params.get("feel"));
  const [facets, setFacets] = useState<Facets | undefined>(() => api.cached<Facets>(`/api/libraries/${libId}/facets?${fq}`));
  useEffect(() => {
    let alive = true;
    api.get<Facets>(`/api/libraries/${libId}/facets?${fq}`).then((f) => alive && setFacets(f), () => {});
    return () => {
      alive = false;
    };
  }, [libId, fq]);
  useEffect(() => onBack(() => (onClose(false), true)), [onClose]);

  const sort = params.get("sort") || "title";
  const si = sortInfo(sort);
  const order = params.get("order") || si.def;
  const facetKeys = facets?.facets.map((f) => f.key) || [...(movies ? ["mood", "content"] : []), "filter", "added", "decade", "rating", "rt", ...(movies ? ["res", "length"] : []), "age"];
  const moodKeys: string[] = facetKeys.filter((k) => k === "mood" || k === "content");
  const nPrefs = prefs.exclude.length + prefs.require.length;
  const cats: { key: string; label: string; value: string }[] = [
    { key: "sort", label: "Sort by", value: si.v === "random" || si.v === "match" ? si.l : `${si.l} · ${order === "asc" ? si.asc : si.desc}` },
    // Mood, content and feel lead in a film library; then genre and the rest.
    ...[...moodKeys, ...(movies ? ["feel"] : []), "genre", ...facetKeys.filter((k) => !moodKeys.includes(k))].map((k) => ({
      key: k,
      label: k === "genre" ? "Genre" : k === "feel" ? "Feel" : facets?.facets.find((f) => f.key === k)?.label || { mood: "Mood", content: "Content", filter: "Watched", added: "Recently added", decade: "Decade", rating: "Rating", rt: "Rotten Tomatoes", res: "Quality", length: "Length", age: "Age rating" }[k] || k,
      value: !params.get(k) ? "Any" : MULTI.has(k) ? params.get(k)!.split(",").map((v) => filterLabel(k, v)).join(", ") : filterLabel(k, params.get(k)!),
    })),
    { key: "never", label: "Never show", value: nPrefs ? [...prefs.exclude.map((g) => `No ${g}`), ...prefs.require.map((g) => `Must be ${g}`)].join(", ") : "Any" },
    ...(movies ? [{ key: "memory", label: "Suggestions", value: mem ? `${mem.resting} resting · ${mem.hidden} hidden` : " " }] : []),
  ];
  const [cat, setCat] = useState(startAt);
  useEffect(() => {
    requestAnimationFrame(() => focusInitial(`cat-${startAt}`));
  }, [startAt]);

  const cur = cat === "sort" ? sort : params.get(cat) || "";
  const multi = MULTI.has(cat);
  const chosen = (v: string) => (multi ? !!v && cur.split(",").includes(v) : v === cur);
  type Opt = { v: string; l: string; n?: string; on?: boolean; go?: () => void };
  const forget = (what: "rules" | "taste", done: string) => () => {
    picks.reset(what).then(() => {
      toast(done);
      picks.state().then(setMem, () => {});
    }, (e: Error) => toast(e.message));
  };
  const opts: Opt[] = cat === "sort"
    ? SORTS.filter((x) => movies || x.v !== "match").map((x) => ({ v: x.v, l: x.l, n: x.v === si.v ? (x.v === "random" ? x.asc : order === "asc" ? x.asc : x.desc) : undefined }))
    : cat === "feel"
      // One press moves a slider a stop along: any → low → middle → high → any.
      ? FEEL.map((d) => {
          const at = feel[d.k];
          const i = at === undefined ? -1 : FEEL_STOPS.findIndex((x) => x >= at);
          const nextStop = FEEL_STOPS[i + 1];
          const nextFeel = { ...feel };
          if (nextStop === undefined) delete nextFeel[d.k];
          else nextFeel[d.k] = nextStop;
          return { v: d.k, l: `${d.l} · ${d.lo} to ${d.hi}`, n: at === undefined ? "Any" : feelWord(d.k, at), on: at !== undefined, go: () => onSet({ feel: feelString(nextFeel) }, `o-feel-${d.k}`) };
        })
      : cat === "never"
        // Off → never (✕) → must have (+) → off, saved for this profile.
        ? allGenres.map((g) => {
            const m = genreMode(prefs, g.name);
            return { v: g.name, l: g.name, n: m === 1 ? "✕ Never" : m === 2 ? "+ Must have" : "", on: m > 0, go: () => onPrefs(cycleGenre(prefs, g.name)) };
          })
        : cat === "memory"
          ? [
              { v: "rules", l: "Bring back rested & hidden films", n: mem ? (mem.resting + mem.hidden).toLocaleString() : "", go: forget("rules", "Rested and hidden films can be suggested again") },
              { v: "taste", l: "Forget my likes & dislikes", n: mem ? mem.votes.toLocaleString() : "", go: forget("taste", "Likes and dislikes forgotten") },
            ]
          : [{ v: "", l: "Any" }, ...((cat === "genre" ? facets?.genres : facets?.facets.find((f) => f.key === cat)?.options) || []).map((o) => ({ v: o.v, l: o.l, n: o.count.toLocaleString() }))];
  const active = activeFilters(params).length;
  const total = facets?.total;

  // On <body>, above the tab bar (the page sits under it).
  return createPortal(
    <div className="tv-modal tv-fp-wrap" data-scope>
      <div className="tv-modal-panel tv-fp">
        <div className="tv-fp-head">
          <h2 className="tv-modal-title">Filter &amp; sort</h2>
          <div className="tv-fp-total">{total === undefined ? " " : `${total.toLocaleString()} ${total === 1 ? "title" : "titles"}`}</div>
        </div>
        <div className="tv-fp-cols">
          <div className="tv-fp-cats" data-group="fp-cats" data-vscroll="reveal" data-margin="1rem">
            {cats.map((c) => (
              <button key={c.key} data-f data-fk={`cat-${c.key}`} className={`tv-fp-cat ${cat === c.key ? "sel" : ""} ${c.value !== "Any" && c.key !== "sort" && c.key !== "memory" ? "set" : ""}`}
                onFocus={() => setCat(c.key)} onClick={() => focusKey(`o-${c.key}-${cur}`) || focusKey(`o-${c.key}-`)}>
                <span className="l">{c.label}</span>
                <span className="v">{c.value}</span>
              </button>
            ))}
          </div>
          <div key={cat} className="tv-fp-opts tv-modal-list" data-group="fp-opts" data-remember="false" data-vscroll="center">
            {opts.map((o, i) => (
              <button key={o.v} data-f data-fk={`o-${cat}-${o.v}`} data-default={(o.go || multi ? i === 0 : o.v === cur) ? "" : undefined}
                className={`tv-list-item ${o.on ?? chosen(o.v) ? "on" : ""} ${o.n === "0" ? "none" : ""}`}
                onClick={() => {
                  const fk = `o-${cat}-${o.v}`;
                  if (o.go) return o.go();
                  if (multi) return onSet({ [cat]: o.v ? toggleIn(cur, o.v) : "" }, fk);
                  if (cat !== "sort") return onSet({ [cat]: o.v === cur ? "" : o.v }, fk);
                  if (o.v === "match") return onSet({ sort: "match", order: "", seed: "" }, fk);
                  if (o.v === "random") return onSet({ sort: "random", order: "", seed: String(Math.floor(Math.random() * 1e9)) }, fk);
                  // The chosen sort again flips its direction.
                  if (o.v === si.v) return onSet({ order: order === "asc" ? "desc" : "asc" }, fk);
                  onSet({ sort: o.v, order: "", seed: "" }, fk);
                }}>
                <span>{o.l}</span>
                {o.n && <span className="tv-muted">{o.n}</span>}
                {!o.go && chosen(o.v) && <Check size={22} />}
              </button>
            ))}
          </div>
        </div>
        <div className="tv-fp-foot" data-group="fp-foot">
          <TvButton primary fk="fp-show" onClick={() => onClose(true)}>
            {total === undefined ? "Show titles" : total === 0 ? "Nothing matches" : `Show ${total.toLocaleString()} ${total === 1 ? "title" : "titles"}`}
          </TvButton>
          {active > 0 && <TvButton fk="fp-clear" onClick={onClear}>Clear all</TvButton>}
        </div>
      </div>
    </div>,
    document.body,
  );
}

/** The letter a title at `pos` in the list sits under. */
function letterOf(letters: Letter[] | undefined, pos: number) {
  let l: string | undefined;
  for (const x of letters || []) if (x.offset <= pos) l = x.l;
  return l;
}

// Drop the oldest remembered grids so trying lots of filters doesn't pile up posters in memory.
function remember(k: string, g: Grid) {
  loaded.delete(k);
  loaded.set(k, g);
  while (loaded.size > 12) loaded.delete(loaded.keys().next().value!);
}

export default function LibraryPage() {
  const { id } = useParams();
  const [params, setParams] = useSearchParams();
  const nav = useNavigate();
  const toast = useToast();
  const sort = params.get("sort") || "title";
  const si = sortInfo(sort);
  const order = params.get("order") || si.def;
  const seed = params.get("seed") || "1";
  const { data: libs } = useLibraries();
  const lib = libs?.find((l) => String(l.id) === id);
  const movies = lib?.kind !== "shows";
  // Which panel category to open on, or null when it's closed.
  const [panel, setPanel] = useState<string | null>(null);

  // "Surprise me" and "Help me choose" (Reel Pick), over a film library.
  // Back from a pick's page reopens it as it was left.
  const pickResume = usePickResume(id, filterQuery(params));
  const [picking, setPicking] = useState<"surprise" | "quiz" | null>(pickResume.initial);
  // The profile's never-show and must-have genres. Saving them changes what
  // the same address returns, so `ver` goes in the address to refetch.
  const [prefs, setPrefs] = useState<GenrePrefs>({ exclude: [], require: [] });
  const [ver, setVer] = useState(0);
  useEffect(() => {
    picks.state().then((st) => setPrefs(st.genres), () => {});
  }, []);
  const savePrefs = useCallback((g: GenrePrefs) => {
    setPrefs(g);
    picks.setGenres(g).then(() => {
      api.invalidate("/api/libraries/");
      loaded.clear();
      setVer((v) => v + 1);
    }, (e: Error) => toast(e.message));
  }, [toast]);

  const fq = filterQuery(params);
  const active = activeFilters(params);
  const base = `/api/libraries/${id}/items?sort=${sort}&order=${order}${sort === "random" ? `&seed=${seed}` : ""}${fq ? `&${fq}` : ""}${ver ? `&_=${ver}` : ""}`;
  const [state, setState] = useState<Grid>(() => loaded.get(base) || { items: [], total: -1, start: 0 });
  const loading = useRef(false);
  const key = useRef(base);

  /** Loads `limit` titles from `offset`: a fresh window (`jump`), or more below or above the current one. */
  const load = useCallback(async (offset: number, how: "jump" | "below" | "above" = "below", limit = PAGE) => {
    if (loading.current) return;
    loading.current = true;
    const myKey = base;
    try {
      const page = await api.get<Page>(`${base}&limit=${limit}&offset=${offset}${offset === 0 && sort === "title" ? "&letters=1" : ""}`);
      if (key.current !== myKey) return;
      setState((prev) => {
        const letters = page.letters || prev.letters;
        const next: Grid = how === "jump" || (offset === 0 && how === "below")
          ? { items: page.items, total: page.total, start: offset, letters }
          : how === "above"
            ? { items: [...page.items, ...prev.items], total: page.total, start: offset, letters }
            : { items: [...prev.items, ...page.items], total: page.total, start: prev.start, letters };
        remember(myKey, next);
        return next;
      });
    } finally {
      if (key.current === myKey) loading.current = false;
    }
  }, [base, sort]);

  useEffect(() => {
    key.current = base;
    loading.current = false;
    const cached = loaded.get(base);
    setState(cached || { items: [], total: -1, start: 0 });
    if (!cached) load(0);
  }, [base, load]);

  // After playback, refetch next time rather than now: reloading would drop
  // the pages you've scrolled through and the focused poster with them.
  const refresh = useCallback(() => {
    loaded.delete(base);
  }, [base]);
  useRefresh(refresh);

  // The grid stays calm: no artwork or trailer behind it as you move
  // (Daniel preferred the plain look of the grid with nothing focused).
  const setBackdrop = useContext(BackdropCtx);
  useEffect(() => {
    setBackdrop(undefined);
    stopTrailer();
  }, [setBackdrop]);
  // Choices made in the panel change the address (a new history entry key),
  // but focus is the panel's business then: don't let the page refocus its
  // first poster when the panel closes.
  const panelOpen = useRef(false);
  panelOpen.current = panel !== null;
  const skipPageFocus = useRef(false);
  usePageFocus(state.total >= 0 && panel === null && picking === null && !skipPageFocus.current);

  const latestParams = useRef(params);
  latestParams.current = params;
  const set = useCallback((changes: Record<string, string>, fk: string) => {
    skipPageFocus.current = panelOpen.current;
    const p = new URLSearchParams(latestParams.current);
    for (const [k, v] of Object.entries(changes)) {
      if (v) p.set(k, v);
      else p.delete(k);
    }
    setParams(p, { replace: true, state: { focus: fk } });
  }, [setParams]);
  const clearAll = useCallback(() => {
    skipPageFocus.current = panelOpen.current;
    const p = new URLSearchParams(latestParams.current);
    FILTER_KEYS.forEach((k) => p.delete(k));
    setParams(p, { replace: true, state: { focus: "filters" } });
    requestAnimationFrame(() => focusKey("filters") || focusKey("fp-show"));
  }, [setParams]);

  // Back from anywhere in the grid (or the A–Z bar) goes straight up to the
  // filters, like Back on Apple TV and Netflix; Back again leaves the page.
  useEffect(() => onBack(() => {
    const el = document.activeElement as HTMLElement | null;
    if (!el || !el.closest(".tv-library") || el.closest(".tv-lib-bar") || el.closest("[data-scope]")) return false;
    return focusKey("filters");
  }), []);

  const latest = useRef({ state, load });
  latest.current = { state, load };
  const onFocusCard = useCallback((c: Card) => {
    const { state: st, load: ld } = latest.current;
    const i = st.items.indexOf(c);
    if (st.start + st.items.length < st.total && i > st.items.length - 40) ld(st.start + st.items.length);
    else if (st.start > 0 && i >= 0 && i < 24) {
      const from = Math.max(0, st.start - PAGE);
      ld(from, "above", st.start - from);
    }
    // Light up the letter you're in on the A–Z bar.
    const l = letterOf(st.letters, st.start + i);
    document.querySelectorAll(".tv-az-letter.on").forEach((el) => el.classList.remove("on"));
    if (l) document.querySelector(`.tv-az-letter[data-fk="az-${CSS.escape(l)}"]`)?.classList.add("on");
  }, []);

  const jump = useCallback(async (l: Letter) => {
    const st = latest.current.state;
    const i = l.offset - st.start;
    // Already loaded: just move there.
    if (i >= 0 && i < st.items.length) return focusKey(`c${st.items[i].id}`);
    await latest.current.load(l.offset, "jump");
    requestAnimationFrame(() => {
      const first = loaded.get(key.current)?.items[0];
      if (first) focusKey(`c${first.id}`);
    });
  }, []);
  const open = useCallback((c: Card) => nav(`/item/${c.id}`), [nav]);
  const playIt = useCallback((c: Card) => playCard(c).catch((e: Error) => toast(e.message)), [toast]);

  const closePanel = useCallback((toGrid: boolean) => {
    setPanel(null);
    // Twice: the grid may still be loading the last choice's titles.
    const go = () => {
      const first = toGrid ? latest.current.state.items[0] : undefined;
      if (!(first && focusKey(`c${first.id}`))) focusKey("filters");
    };
    requestAnimationFrame(go);
  }, []);

  const quick = QUICK.filter((q) => movies || !q.movies);
  // Filters that are on but have no quick chip get a chip of their own, so
  // what's narrowing the list is always in view and one OK removes it.
  const pills = active.filter((a) => !quick.some((q) => q.k === a.k && q.v === a.v));
  const genrePills = [...prefs.exclude.map((g) => ({ g, l: `No ${g}` })), ...prefs.require.map((g) => ({ g, l: `Must be ${g}` }))];
  const closePicking = useCallback(() => {
    setPicking((was) => {
      requestAnimationFrame(() => focusKey(was === "quiz" ? "choose" : "surprise") || focusKey("filters"));
      return null;
    });
    pickResume.end();
    // Watchlist, Seen it and the picks' memory may have changed what the grid shows.
    loaded.delete(key.current);
  }, [pickResume.end]);
  const sortChip = si.v === "random" || si.v === "match" ? si.l : si.v === "title" ? (order === "asc" ? "A–Z" : "Z–A") : `${si.l} · ${order === "asc" ? si.asc : si.desc}`;
  const total = state.total;

  return (
    <div className="tv-page tv-library" data-vscroll="reveal" data-margin="5rem">
      <header className="tv-lib-head" data-row data-row-top>
        <div className="tv-lib-title">
          <h1 className="tv-h1">{lib?.name || "Library"}</h1>
          <div className="tv-muted tv-count">
            {total < 0 ? " " : (active.length || genrePills.length) && lib?.count ? `${total.toLocaleString()} of ${lib.count.toLocaleString()} titles` : `${total.toLocaleString()} titles`}
          </div>
        </div>
        <div className="tv-lib-bar" data-group="lib-bar">
          <TvButton fk="filters" className={`chip tv-lib-filters ${active.length ? "on" : ""}`} icon={<Sliders size={22} />}
            onClick={() => setPanel(movies ? "mood" : "genre")}>{active.length ? `Filters · ${active.length}` : "Filters"}</TvButton>
          <TvButton fk="sort" className="chip" icon={si.v === "random" ? <Shuffle size={22} /> : <SortIcon size={22} />}
            onClick={() => setPanel("sort")}>{sortChip}</TvButton>
          {movies && (
            <>
              <TvButton fk="surprise" className="chip tv-pick-chip" icon={<Dice size={22} />} onClick={() => setPicking("surprise")}>Surprise me</TvButton>
              <TvButton fk="choose" className="chip tv-pick-chip" icon={<Compass size={22} />} onClick={() => setPicking("quiz")}>Help me choose</TvButton>
            </>
          )}
          <span className="tv-sep" />
          {quick.map((q) => {
            const on = params.get(q.k) === q.v;
            return (
              <TvButton key={`${q.k}-${q.v}`} fk={`q-${q.k}-${q.v}`} className={`chip ${on ? "on" : ""}`}
                onClick={() => set({ [q.k]: on ? "" : q.v }, `q-${q.k}-${q.v}`)}>{q.l}</TvButton>
            );
          })}
          {pills.map((a) => (
            <TvButton key={`${a.k}-${a.v}`} fk={`p-${a.k}${MULTI.has(a.k) ? `-${a.v.split(":")[0]}` : ""}`} className="chip on tv-pill" onClick={() => set({ [a.k]: a.without }, "filters")}>
              {`${a.label}  ✕`}
            </TvButton>
          ))}
          {genrePills.map((x) => (
            <TvButton key={x.g} fk={`pg-${x.g}`} className="chip on tv-pill" onClick={() => (savePrefs(cycleGenre(prefs, x.g, 0)), focusKey("filters"))}>
              {`${x.l}  ✕`}
            </TvButton>
          ))}
          {active.length > 1 && <TvButton fk="clear" className="chip tv-clear" onClick={clearAll}>Clear all</TvButton>}
        </div>
      </header>
      {state.total < 0 ? (
        <Spinner />
      ) : state.total === 0 ? (
        <div className="tv-center small"><p className="tv-muted">{active.length || genrePills.length ? "Nothing matches these filters." : "Nothing here yet."}</p></div>
      ) : (
        <div className="tv-grid">
          {state.items.map((c, i) => (
            <div key={c.id} data-row>
              <TvCard c={c} shape="poster" autoFocus={i === 0} onSelect={open} onPlay={playIt} onFocusCard={onFocusCard}
                sub={c.match ? `${c.year || ""}${c.year ? " · " : ""}${c.match}% match` : undefined} />
            </div>
          ))}
        </div>
      )}
      {sort === "title" && state.letters && state.letters.length > 1 && state.total > 48 && (
        // Entered only with Right from the grid, so Up and Down stay on the
        // letters; the button on top opens the filters.
        <nav className="tv-az" data-group="az" data-enter="right" aria-label="Jump to letter">
          <button className="tv-az-letter tv-az-filters" data-f data-fk="az-filters" aria-label="Filters" onClick={() => setPanel(movies ? "mood" : "genre")}>
            <Sliders size={18} />
          </button>
          {state.letters.map((l) => (
            <button key={l.l} className="tv-az-letter" data-f data-fk={`az-${l.l}`} onClick={() => jump(l)}>{l.l}</button>
          ))}
        </nav>
      )}
      {panel !== null && id && (
        <FilterPanel libId={id} movies={movies} params={params} startAt={panel} onSet={set} onClear={clearAll} onClose={closePanel}
          prefs={prefs} onPrefs={savePrefs} ver={ver} />
      )}
      {picking === "surprise" && id && (
        <SurpriseOverlay libId={id} filters={fq} label={params.get("mood") ? moodLabel(params.get("mood")!) : active.length ? "From your filters" : "Surprise me"} resume={pickResume.resume} onClose={closePicking} />
      )}
      {picking === "quiz" && id && <QuizOverlay libId={id} prefs={prefs} onPrefs={savePrefs} resume={pickResume.resume} onClose={closePicking} />}
    </div>
  );
}
