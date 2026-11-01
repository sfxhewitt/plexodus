import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { api, art, Card } from "../../api";
import { Check } from "../../components/Icons";
import { useApi } from "../../hooks";
import { Spinner, TvButton, TvCard, useBackdrop } from "../components";
import { focusInitial, focusKey, onBack } from "../focus";
import { useLibraries, usePageFocus, useRefresh, useToast } from "../hooks";
import { playCard } from "../play";

const PAGE = 120;
type Page = { items: Card[]; total: number; offset: number };

// Loaded pages per query, so coming back from a title restores the grid
// (and the focused poster) however far down you'd scrolled.
const loaded = new Map<string, { items: Card[]; total: number }>();

const SORTS: [string, string][] = [["title", "A–Z"], ["added", "Recently added"], ["release", "Release date"], ["rating", "Rating"], ["year", "Year"]];
const FILTERS: [string, string][] = [["", "All"], ["unwatched", "Unwatched"], ["inprogress", "In progress"]];

function GenrePicker({ genres, current, onPick, onClose }: {
  genres: { name: string; count: number }[]; current: string; onPick: (g: string) => void; onClose: () => void;
}) {
  useEffect(() => onBack(() => (onClose(), true)), [onClose]);
  useEffect(() => {
    requestAnimationFrame(() => focusInitial(current ? `g-${current}` : "g-"));
  }, [current]);
  return (
    <div className="tv-modal" data-scope>
      <div className="tv-modal-panel">
        <h2 className="tv-modal-title">Genre</h2>
        <div className="tv-modal-list" data-vscroll="center">
          {[{ name: "", count: 0 }, ...genres].map((g) => (
            <button key={g.name} data-f data-fk={`g-${g.name}`} className={`tv-list-item ${g.name === current ? "on" : ""}`} onClick={() => onPick(g.name)}>
              <span>{g.name || "All genres"}</span>
              {g.count ? <span className="tv-muted">{g.count.toLocaleString()}</span> : null}
              {g.name === current && <Check size={22} />}
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}

export default function LibraryPage() {
  const { id } = useParams();
  const [params, setParams] = useSearchParams();
  const nav = useNavigate();
  const toast = useToast();
  const sort = params.get("sort") || "title";
  const order = params.get("order") || (sort === "title" ? "asc" : "desc");
  const genre = params.get("genre") || "";
  const filter = params.get("filter") || "";
  const { data: libs } = useLibraries();
  const { data: genres } = useApi<{ name: string; count: number }[]>(`/api/libraries/${id}/genres`);
  const lib = libs?.find((l) => String(l.id) === id);
  const [picking, setPicking] = useState(false);

  const base = `/api/libraries/${id}/items?sort=${sort}&order=${order}&genre=${encodeURIComponent(genre)}&filter=${filter}&limit=${PAGE}`;
  const [state, setState] = useState(() => loaded.get(base) || { items: [] as Card[], total: -1 });
  const loading = useRef(false);
  const key = useRef(base);

  const load = useCallback(async (offset: number) => {
    if (loading.current) return;
    loading.current = true;
    const myKey = base;
    try {
      const page = await api.get<Page>(`${base}&offset=${offset}`);
      if (key.current !== myKey) return;
      setState((prev) => {
        const next = { items: offset === 0 ? page.items : [...prev.items, ...page.items], total: page.total };
        loaded.set(myKey, next);
        return next;
      });
    } finally {
      loading.current = false;
    }
  }, [base]);

  useEffect(() => {
    key.current = base;
    loading.current = false;
    const cached = loaded.get(base);
    setState(cached || { items: [], total: -1 });
    if (!cached) load(0);
  }, [base, load]);

  // After playback, refetch next time rather than now: reloading would drop
  // the pages you've scrolled through and the focused poster with them.
  const refresh = useCallback(() => {
    loaded.delete(base);
  }, [base]);
  useRefresh(refresh);

  const [focused, setFocused] = useState<Card>();
  useBackdrop(art(focused?.backdrop, "w1280"), 350);
  usePageFocus(state.total >= 0);

  const set = (k: string, v: string, fk: string) => {
    const p = new URLSearchParams(params);
    if (v) p.set(k, v);
    else p.delete(k);
    if (k === "sort") p.delete("order");
    setParams(p, { replace: true, state: { focus: fk } });
  };

  const onFocusCard = (c: Card, i: number) => {
    setFocused(c);
    if (i > state.items.length - 40 && state.items.length < state.total) load(state.items.length);
  };

  const closePicker = useCallback(() => {
    setPicking(false);
    requestAnimationFrame(() => focusKey("genre"));
  }, []);

  const sortLabel = SORTS.find(([v]) => v === sort)?.[1] || "A–Z";
  const nextSort = SORTS[(SORTS.findIndex(([v]) => v === sort) + 1) % SORTS.length][0];

  return (
    <div className="tv-page tv-library" data-vscroll="reveal" data-margin="5rem">
      <header className="tv-lib-head" data-row data-row-top>
        <div>
          <h1 className="tv-h1">{lib?.name || "Library"}</h1>
          <div className="tv-muted tv-count">{state.total >= 0 ? `${state.total.toLocaleString()} titles` : " "}</div>
        </div>
        <div className="tv-lib-controls" data-group="lib-controls">
          {FILTERS.map(([v, l]) => (
            <TvButton key={v || "all"} fk={`f-${v}`} className={`chip ${filter === v ? "on" : ""}`} onClick={() => set("filter", v, `f-${v}`)}>{l}</TvButton>
          ))}
          <span className="tv-sep" />
          <TvButton fk="sort" className="chip" onClick={() => set("sort", nextSort, "sort")}>Sort: {sortLabel}</TvButton>
          <TvButton fk="order" className="chip" onClick={() => set("order", order === "asc" ? "desc" : "asc", "order")}>{order === "asc" ? "↑" : "↓"}</TvButton>
          {genres && genres.length > 0 && (
            <TvButton fk="genre" className={`chip ${genre ? "on" : ""}`} onClick={() => setPicking(true)}>{genre || "All genres"}</TvButton>
          )}
        </div>
      </header>
      {state.total < 0 ? (
        <Spinner />
      ) : state.total === 0 ? (
        <div className="tv-center small"><p className="tv-muted">Nothing here{filter || genre ? " with these filters" : " yet"}.</p></div>
      ) : (
        <div className="tv-grid">
          {state.items.map((c, i) => (
            <div key={c.id} data-row>
              <TvCard c={c} shape="poster" autoFocus={i === 0} onSelect={() => nav(`/item/${c.id}`)}
                onPlay={(x) => playCard(x).catch((e: Error) => toast(e.message))} onFocusCard={(x) => onFocusCard(x, i)} />
            </div>
          ))}
        </div>
      )}
      {picking && genres && (
        <GenrePicker genres={genres} current={genre} onClose={closePicker}
          onPick={(g) => { setPicking(false); set("genre", g, "genre"); }} />
      )}
    </div>
  );
}
