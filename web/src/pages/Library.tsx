import { useCallback, useEffect, useRef, useState } from "react";
import { useParams, useSearchParams } from "react-router-dom";
import { api, Card, Library as Lib } from "../api";
import { PosterCard } from "../components/Cards";
import { useApi } from "../hooks";

const PAGE = 120;

type Page = { items: Card[]; total: number; offset: number };

export default function Library() {
  const { id } = useParams();
  const [params, setParams] = useSearchParams();
  const sort = params.get("sort") || "title";
  const order = params.get("order") || (sort === "title" ? "asc" : "desc");
  const genre = params.get("genre") || "";
  const filter = params.get("filter") || "";
  const { data: libs } = useApi<Lib[]>("/api/libraries");
  const { data: genres } = useApi<{ name: string; count: number }[]>(`/api/libraries/${id}/genres`);
  const lib = libs?.find((l) => String(l.id) === id);

  const base = `/api/libraries/${id}/items?sort=${sort}&order=${order}&genre=${encodeURIComponent(genre)}&filter=${filter}&limit=${PAGE}`;
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
          <select value={genre} onChange={(e) => set("genre", e.target.value)} aria-label="Genre">
            <option value="">All genres</option>
            {genres?.map((g) => <option key={g.name} value={g.name}>{g.name} ({g.count})</option>)}
          </select>
          <select value={sort} onChange={(e) => set("sort", e.target.value)} aria-label="Sort">
            <option value="title">Title</option>
            <option value="added">Recently added</option>
            <option value="release">Release date</option>
            <option value="rating">Rating</option>
            <option value="year">Year</option>
          </select>
          <button className="btn glass sm" onClick={() => set("order", order === "asc" ? "desc" : "asc")}
            title="Reverse order">{order === "asc" ? "↑" : "↓"}</button>
        </div>
      </div>
      <div className="grid">
        {items.map((c) => <PosterCard key={c.id} c={c} />)}
        {total === null && items.length === 0 &&
          Array.from({ length: 24 }, (_, i) => <div key={i} className="poster-card skeleton"><div className="poster-frame" /></div>)}
      </div>
      {total === 0 && <div className="page-msg">Nothing here yet. If you just added this library, it's still scanning.</div>}
      <div ref={sentinel} style={{ height: 1 }} />
    </div>
  );
}
