import { useCallback, useContext, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { api, art, Card } from "../../api";
import { Check, Shuffle, Sliders, SortIcon } from "../../components/Icons";
import { BackdropCtx, Spinner, TvButton, TvCard } from "../components";
import { focusInitial, focusKey, onBack } from "../focus";
import { useLibraries, usePageFocus, useRefresh, useToast } from "../hooks";
import { requestTrailer, stopTrailer } from "../ambient";
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
  { v: "runtime", l: "Length", asc: "Shortest first", desc: "Longest first", def: "asc" },
  { v: "random", l: "Shuffle", asc: "Pick again to reshuffle", desc: "Pick again to reshuffle", def: "asc" },
];
const sortInfo = (v: string) => SORTS.find((x) => x.v === v) || (v === "year" ? { ...SORTS[2], v: "year", l: "Year" } : SORTS[0]);

// Every filter the server understands (internal/api/libfilter.go), with labels
// for the chips that show what's on. Counts come from /facets.
const FILTER_KEYS = ["filter", "genre", "added", "decade", "rating", "res", "length", "age"] as const;
type FilterKey = (typeof FILTER_KEYS)[number];
const LABELS: Record<string, Record<string, string>> = {
  filter: { unwatched: "Unwatched", inprogress: "In progress", watched: "Watched" },
  added: { "7": "Added this week", "30": "New this month", "90": "Added in 3 months" },
  rating: { "8": "Rated 8+", "7": "Rated 7+", "6": "Rated 6+", "5": "Rated 5+" },
  res: { "4k": "4K", "1080": "1080p", "720": "720p", sd: "SD" },
  length: { short: "Under 90 min", medium: "90 min – 2 h", long: "Over 2 h" },
  age: { kids: "Kids", "13": "Up to 13", "16": "Up to 16", adult: "Adults" },
};
function filterLabel(k: string, v: string) {
  if (k === "genre") return v;
  if (k === "decade") return v === "0" ? "Before 1950" : `${v}s`;
  return LABELS[k]?.[v] || v;
}

// One-press shortcuts in the bar, like the quick filters on Apple TV and Netflix.
const QUICK: { k: FilterKey; v: string; l: string; movies?: boolean }[] = [
  { k: "filter", v: "unwatched", l: "Unwatched" },
  { k: "filter", v: "inprogress", l: "In progress" },
  { k: "added", v: "30", l: "New this month" },
  { k: "rating", v: "7", l: "Highly rated" },
  { k: "res", v: "4k", l: "4K", movies: true },
  { k: "length", v: "short", l: "Under 90 min", movies: true },
];

type FacetOpt = { v: string; l: string; count: number };
type Facets = { total: number; facets: { key: string; label: string; options: FacetOpt[] }[]; genres: FacetOpt[] };

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
function FilterPanel({ libId, movies, params, startAt, onSet, onClear, onClose }: {
  libId: string; movies: boolean; params: URLSearchParams; startAt: string;
  onSet: (changes: Record<string, string>, fk: string) => void; onClear: () => void; onClose: (toGrid: boolean) => void;
}) {
  const fq = filterQuery(params);
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
  const cats: { key: string; label: string; value: string }[] = [
    { key: "sort", label: "Sort by", value: si.v === "random" ? "Shuffle" : `${si.l} · ${order === "asc" ? si.asc : si.desc}` },
    ...(["genre", ...(facets?.facets.map((f) => f.key) || ["filter", "added", "decade", "rating", ...(movies ? ["res", "length"] : []), "age"])]).map((k) => ({
      key: k,
      label: k === "genre" ? "Genre" : facets?.facets.find((f) => f.key === k)?.label || { filter: "Watched", added: "Recently added", decade: "Decade", rating: "Rating", res: "Quality", length: "Length", age: "Age rating" }[k] || k,
      value: params.get(k) ? filterLabel(k, params.get(k)!) : "Any",
    })),
  ];
  const [cat, setCat] = useState(startAt);
  useEffect(() => {
    requestAnimationFrame(() => focusInitial(`cat-${startAt}`));
  }, [startAt]);

  const cur = cat === "sort" ? sort : params.get(cat) || "";
  const opts: { v: string; l: string; n?: string }[] = cat === "sort"
    ? SORTS.map((x) => ({ v: x.v, l: x.l, n: x.v === si.v ? (x.v === "random" ? x.asc : order === "asc" ? x.asc : x.desc) : undefined }))
    : [{ v: "", l: "Any" }, ...((cat === "genre" ? facets?.genres : facets?.facets.find((f) => f.key === cat)?.options) || []).map((o) => ({ v: o.v, l: o.l, n: o.count.toLocaleString() }))];
  const active = FILTER_KEYS.filter((k) => params.get(k)).length;
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
          <div className="tv-fp-cats" data-group="fp-cats">
            {cats.map((c) => (
              <button key={c.key} data-f data-fk={`cat-${c.key}`} className={`tv-fp-cat ${cat === c.key ? "sel" : ""} ${c.value !== "Any" && c.key !== "sort" ? "set" : ""}`}
                onFocus={() => setCat(c.key)} onClick={() => focusKey(`o-${c.key}-${cur}`) || focusKey(`o-${c.key}-`)}>
                <span className="l">{c.label}</span>
                <span className="v">{c.value}</span>
              </button>
            ))}
          </div>
          <div key={cat} className="tv-fp-opts tv-modal-list" data-group="fp-opts" data-remember="false" data-vscroll="center">
            {opts.map((o) => (
              <button key={o.v} data-f data-fk={`o-${cat}-${o.v}`} data-default={o.v === cur ? "" : undefined}
                className={`tv-list-item ${o.v === cur ? "on" : ""} ${o.n === "0" ? "none" : ""}`}
                onClick={() => {
                  const fk = `o-${cat}-${o.v}`;
                  if (cat !== "sort") return onSet({ [cat]: o.v === cur ? "" : o.v }, fk);
                  if (o.v === "random") return onSet({ sort: "random", order: "", seed: String(Math.floor(Math.random() * 1e9)) }, fk);
                  // The chosen sort again flips its direction.
                  if (o.v === si.v) return onSet({ order: order === "asc" ? "desc" : "asc" }, fk);
                  onSet({ sort: o.v, order: "", seed: "" }, fk);
                }}>
                <span>{o.l}</span>
                {o.n && <span className="tv-muted">{o.n}</span>}
                {o.v === cur && <Check size={22} />}
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

  const fq = filterQuery(params);
  const active = FILTER_KEYS.filter((k) => params.get(k));
  const base = `/api/libraries/${id}/items?sort=${sort}&order=${order}${sort === "random" ? `&seed=${seed}` : ""}${fq ? `&${fq}` : ""}`;
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

  // The focused title's art fades in behind the grid once you pause on it.
  // Set directly rather than through state: re-rendering the page on every
  // move re-rendered every poster in the grid.
  const setBackdrop = useContext(BackdropCtx);
  const bdTimer = useRef<ReturnType<typeof setTimeout>>();
  useEffect(() => () => {
    clearTimeout(bdTimer.current);
    setBackdrop(undefined);
    stopTrailer();
  }, [setBackdrop]);
  // Choices made in the panel change the address (a new history entry key),
  // but focus is the panel's business then: don't let the page refocus its
  // first poster when the panel closes.
  const panelOpen = useRef(false);
  panelOpen.current = panel !== null;
  const skipPageFocus = useRef(false);
  usePageFocus(state.total >= 0 && panel === null && !skipPageFocus.current);

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
    clearTimeout(bdTimer.current);
    bdTimer.current = setTimeout(() => setBackdrop(art(c.backdrop, "w1280")), 350);
    // Rest on a poster a little longer and its trailer plays behind the grid.
    requestTrailer(c.kind === "movie" || c.kind === "show" ? c.id : undefined, 3000);
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
  }, [setBackdrop]);

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
  const pills = active.filter((k) => !quick.some((q) => q.k === k && q.v === params.get(k)));
  const sortChip = si.v === "random" ? "Shuffle" : si.v === "title" ? (order === "asc" ? "A–Z" : "Z–A") : `${si.l} · ${order === "asc" ? si.asc : si.desc}`;
  const total = state.total;

  return (
    <div className="tv-page tv-library" data-vscroll="reveal" data-margin="5rem">
      <header className="tv-lib-head" data-row data-row-top>
        <div className="tv-lib-title">
          <h1 className="tv-h1">{lib?.name || "Library"}</h1>
          <div className="tv-muted tv-count">
            {total < 0 ? " " : active.length && lib?.count ? `${total.toLocaleString()} of ${lib.count.toLocaleString()} titles` : `${total.toLocaleString()} titles`}
          </div>
        </div>
        <div className="tv-lib-bar" data-group="lib-bar">
          <TvButton fk="filters" className={`chip tv-lib-filters ${active.length ? "on" : ""}`} icon={<Sliders size={22} />}
            onClick={() => setPanel("genre")}>{active.length ? `Filters · ${active.length}` : "Filters"}</TvButton>
          <TvButton fk="sort" className="chip" icon={si.v === "random" ? <Shuffle size={22} /> : <SortIcon size={22} />}
            onClick={() => setPanel("sort")}>{sortChip}</TvButton>
          <span className="tv-sep" />
          {quick.map((q) => {
            const on = params.get(q.k) === q.v;
            return (
              <TvButton key={`${q.k}-${q.v}`} fk={`q-${q.k}-${q.v}`} className={`chip ${on ? "on" : ""}`}
                onClick={() => set({ [q.k]: on ? "" : q.v }, `q-${q.k}-${q.v}`)}>{q.l}</TvButton>
            );
          })}
          {pills.map((k) => (
            <TvButton key={k} fk={`p-${k}`} className="chip on tv-pill" onClick={() => set({ [k]: "" }, "filters")}>
              {`${filterLabel(k, params.get(k)!)}  ✕`}
            </TvButton>
          ))}
          {active.length > 1 && <TvButton fk="clear" className="chip tv-clear" onClick={clearAll}>Clear all</TvButton>}
        </div>
      </header>
      {state.total < 0 ? (
        <Spinner />
      ) : state.total === 0 ? (
        <div className="tv-center small"><p className="tv-muted">{active.length ? "Nothing matches these filters." : "Nothing here yet."}</p></div>
      ) : (
        <div className="tv-grid">
          {state.items.map((c, i) => (
            <div key={c.id} data-row>
              <TvCard c={c} shape="poster" autoFocus={i === 0} onSelect={open} onPlay={playIt} onFocusCard={onFocusCard} />
            </div>
          ))}
        </div>
      )}
      {sort === "title" && state.letters && state.letters.length > 1 && state.total > 48 && (
        // Entered only with Right from the grid, so Up and Down stay on the
        // letters; the button on top opens the filters.
        <nav className="tv-az" data-group="az" data-enter="right" aria-label="Jump to letter">
          <button className="tv-az-letter tv-az-filters" data-f data-fk="az-filters" aria-label="Filters" onClick={() => setPanel("genre")}>
            <Sliders size={18} />
          </button>
          {state.letters.map((l) => (
            <button key={l.l} className="tv-az-letter" data-f data-fk={`az-${l.l}`} onClick={() => jump(l)}>{l.l}</button>
          ))}
        </nav>
      )}
      {panel !== null && id && (
        <FilterPanel libId={id} movies={movies} params={params} startAt={panel} onSet={set} onClear={clearAll} onClose={closePanel} />
      )}
    </div>
  );
}
