import { useCallback, useEffect, useRef, useState } from "react";
import { useParams, useSearchParams } from "react-router-dom";
import { api, Card, Library as Lib } from "../api";
import { PosterCard } from "../components/Cards";
import { Compass, Dice, Sliders, X } from "../components/Icons";
import { activeFilters, filterQuery, FilterSheet } from "../components/LibraryFilters";
import { QuizSheet, SurpriseSheet } from "../components/Picks";
import { useApi } from "../hooks";
import { cycleGenre, GenrePrefs, picks } from "../picks";
import { usePickResume } from "../picksHooks";

const PAGE = 120;

type Page = { items: Card[]; total: number; offset: number };

export default function Library() {
  const { id } = useParams();
  const [params, setParams] = useSearchParams();
  const sort = params.get("sort") || "title";
  const order = params.get("order") || (sort === "title" ? "asc" : "desc");
  const filter = params.get("filter") || "";
  const { data: libs } = useApi<Lib[]>("/api/libraries");
  const { data: genres } = useApi<{ name: string; count: number }[]>(`/api/libraries/${id}/genres`);
  const lib = libs?.find((l) => String(l.id) === id);
  const movies = lib?.kind === "movies";

  // Reel Pick: the filter sheet, "Surprise me" and "Help me choose".
  // Back from a pick's page (or from playing it) reopens it as it was left.
  const pickResume = usePickResume(id, filterQuery(params));
  const [sheet, setSheet] = useState<"filters" | "surprise" | "quiz" | null>(pickResume.initial);
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
      setVer((v) => v + 1);
    }, () => {});
  }, []);
  // Watchlist, Seen it and hidden films may have changed what the grid shows.
  const closeSheet = useCallback(() => {
    if (sheet === "surprise" || sheet === "quiz") {
      setVer((v) => v + 1);
      pickResume.end();
    }
    setSheet(null);
  }, [sheet, pickResume.end]);

  const fq = filterQuery(params);
  const pills = activeFilters(params, ["filter"]);
  const base = `/api/libraries/${id}/items?sort=${sort}&order=${order}${fq ? `&${fq}` : ""}${ver ? `&_=${ver}` : ""}&limit=${PAGE}`;
  const [items, setItems] = useState<Card[]>(() => api.cached<Page>(base + "&offset=0")?.items || []);
  const [total, setTotal] = useState<number | null>(null);
  const loading = useRef(false);
  const sentinel = useRef<HTMLDivElement>(null);
  const key = useRef(base);

  const load = useCallback(
    async (offset: number) => {
      if (loading.current) return;
      loading.current = true;
      const myKey = base;
      try {
        const page = await api.get<Page>(`${base}&offset=${offset}`);
        if (key.current !== myKey) return;
        setTotal(page.total);
        setItems((prev) => (offset === 0 ? page.items : [...prev, ...page.items]));
      } finally {
        loading.current = false;
      }
    },
    [base],
  );

  useEffect(() => {
    key.current = base;
    loading.current = false;
    setItems(api.cached<Page>(base + "&offset=0")?.items || []);
    setTotal(null);
    load(0);
  }, [base, load]);

  // Infinite scroll: fetch the next page well before the user reaches the end.
  useEffect(() => {
    const el = sentinel.current;
    if (!el) return;
    const io = new IntersectionObserver(
      (es) => {
        if (es[0].isIntersecting && total !== null && items.length < total) load(items.length);
      },
      { rootMargin: "1500px" },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [items.length, total, load]);

  const set = (k: string, v: string) => {
    const p = new URLSearchParams(params);
    if (v) p.set(k, v);
    else p.delete(k);
    if (k === "sort") p.delete("order");
    setParams(p, { replace: true });
  };

  return (
    <div className="library page">
      <div className="lib-head">
        <div>
          <h1>{lib?.name || "Library"}</h1>
          <div className="muted">{total !== null ? `${total.toLocaleString()} titles` : " "}</div>
        </div>
        <div className="lib-controls">
          <div className="seg">
            {[["", "All"], ["unwatched", "Unwatched"], ["inprogress", "In progress"]].map(([v, l]) => (
              <button key={v} className={filter === v ? "on" : ""} onClick={() => set("filter", v)}>{l}</button>
            ))}
          </div>
          <button className={`btn glass sm ${pills.length ? "on" : ""}`} onClick={() => setSheet("filters")}>
            <Sliders size={16} /> Filters{pills.length ? ` · ${pills.length}` : ""}
          </button>
          <select value={sort} onChange={(e) => set("sort", e.target.value)} aria-label="Sort">
            <option value="title">Title</option>
            <option value="added">Recently added</option>
            <option value="release">Release date</option>
            <option value="rating">Rating</option>
            <option value="critics">Tomatometer</option>
            <option value="audience">Audience score</option>
            <option value="year">Year</option>
            <option value="runtime">Length</option>
            {movies && <option value="match">Best match</option>}
          </select>
          {sort !== "match" && <button className="btn glass sm" onClick={() => set("order", order === "asc" ? "desc" : "asc")}
            title="Reverse order">{order === "asc" ? "↑" : "↓"}</button>}
        </div>
      </div>
      {movies && (
        <div className="lib-picks">
          <button className="btn pick" onClick={() => setSheet("surprise")}><Dice size={18} /> Surprise me</button>
          <button className="btn pick" onClick={() => setSheet("quiz")}><Compass size={18} /> Help me choose</button>
        </div>
      )}
      {(pills.length > 0 || prefs.exclude.length > 0 || prefs.require.length > 0) && (
        <div className="lib-pills">
          {pills.map((a) => (
            <button key={`${a.k}-${a.v}`} className="pk-chip on pill" onClick={() => set(a.k, a.without)} aria-label={`Remove filter: ${a.label}`}>{a.label} <X size={13} /></button>
          ))}
          {prefs.exclude.map((g) => (
            <button key={`x${g}`} className="pk-chip g1 pill" onClick={() => savePrefs(cycleGenre(prefs, g, 0))} aria-label={`Allow ${g} again`}>{g} <X size={13} /></button>
          ))}
          {prefs.require.map((g) => (
            <button key={`r${g}`} className="pk-chip g2 pill" onClick={() => savePrefs(cycleGenre(prefs, g, 0))} aria-label={`Stop insisting on ${g}`}>+ {g} <X size={13} /></button>
          ))}
        </div>
      )}
      <div className="grid">
        {items.map((c) => <PosterCard key={c.id} c={c} subtitle={c.match ? `${c.match}% match${c.year ? ` · ${c.year}` : ""}` : undefined} />)}
        {total === null && items.length === 0 &&
          Array.from({ length: 24 }, (_, i) => <div key={i} className="poster-card skeleton"><div className="poster-frame" /></div>)}
      </div>
      {total === 0 && <div className="page-msg">{pills.length || filter || prefs.exclude.length || prefs.require.length
        ? "Nothing matches these filters. Loosen one and try again."
        : "Nothing here yet. If you just added this library, it's still scanning."}</div>}
      <div ref={sentinel} style={{ height: 1 }} />
      {sheet === "filters" && id && (
        <FilterSheet libId={id} movies={movies} params={params} prefs={prefs} onPrefs={savePrefs} onClose={closeSheet}
          onApply={(d) => (setParams(d, { replace: true }), setSheet(null))} />
      )}
      {sheet === "surprise" && id && <SurpriseSheet libId={id} filters={fq} label="Surprise me" resume={pickResume.resume} onClose={closeSheet} />}
      {sheet === "quiz" && id && (
        <QuizSheet libId={id} genres={[...new Set([...(genres?.map((g) => g.name) || []), ...prefs.exclude, ...prefs.require])].sort()}
          prefs={prefs} onPrefs={savePrefs} resume={pickResume.resume} onClose={closeSheet} />
      )}
    </div>
  );
}
