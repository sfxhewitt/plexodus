import { useEffect, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Card } from "../../api";
import { Search as SearchIcon } from "../../components/Icons";
import { useApi, useDebounced } from "../../hooks";
import { TvCard, TvRow, useBackdrop } from "../components";
import { usePageFocus, useToast } from "../hooks";
import { playItem } from "../play";

type Results = { movies: Card[]; shows: Card[]; episodes: Card[] };
const KEYS = "abcdefghijklmnopqrstuvwxyz1234567890".split("");

export default function SearchPage() {
  const [params, setParams] = useSearchParams();
  const nav = useNavigate();
  const toast = useToast();
  const [q, setQ] = useState(() => params.get("q") || "");
  const dq = useDebounced(q.trim(), 250);
  const { data } = useApi<Results>(dq ? `/api/search?q=${encodeURIComponent(dq)}` : null);
  useBackdrop(undefined);
  usePageFocus(true);

  // Keep the query in the URL so Back from a result comes back to it.
  useEffect(() => {
    if ((params.get("q") || "") !== dq) setParams(dq ? { q: dq } : {}, { replace: true, state: { focus: document.activeElement instanceof HTMLElement ? document.activeElement.dataset.fk : undefined } });
  }, [dq]);

  const type = (k: string) => setQ((v) => (v + k).slice(0, 60));
  const results = data && dq ? data : undefined;
  const empty = results && !results.movies.length && !results.shows.length && !results.episodes.length;

  return (
    <div className="tv-search">
      <div className="tv-kb" data-group="kb" data-remember="true">
        <label className="tv-search-input">
          <SearchIcon size={26} />
          <input data-f data-fk="input" value={q} placeholder="Search titles" onChange={(e) => setQ(e.target.value)} />
        </label>
        <div className="tv-keys">
          {KEYS.map((k, i) => (
            <button key={k} data-f data-fk={`k-${k}`} data-autofocus={i === 0 ? "" : undefined} className="tv-key" onClick={() => type(k)}>{k}</button>
          ))}
          <button data-f data-fk="k-space" className="tv-key wide" onClick={() => type(" ")}>Space</button>
          <button data-f data-fk="k-del" className="tv-key wide" onClick={() => setQ((v) => v.slice(0, -1))}>Delete</button>
          <button data-f data-fk="k-clear" className="tv-key wide" onClick={() => setQ("")}>Clear</button>
        </div>
      </div>
      <div className="tv-search-results" data-vscroll="reveal" data-margin="3rem">
        {!dq && <div className="tv-search-hint"><h1 className="tv-h1">Search</h1><p className="tv-muted">Type with the keys on the left, or select the box to use your remote's keyboard or voice search.</p></div>}
        {empty && <div className="tv-search-hint"><p className="tv-muted">Nothing matches “{dq}”.</p></div>}
        {results && results.movies.length > 0 && (
          <TvRow id="sr-movies" title="Movies">
            {results.movies.map((c) => <TvCard key={c.id} c={c} shape="poster" onSelect={() => nav(`/item/${c.id}`)} onPlay={() => playItem(c.id).catch((e: Error) => toast(e.message))} />)}
          </TvRow>
        )}
        {results && results.shows.length > 0 && (
          <TvRow id="sr-shows" title="Shows">
            {results.shows.map((c) => <TvCard key={c.id} c={c} shape="poster" onSelect={() => nav(`/item/${c.id}`)} />)}
          </TvRow>
        )}
        {results && results.episodes.length > 0 && (
          <TvRow id="sr-eps" title="Episodes">
            {results.episodes.map((c) => <TvCard key={c.id} c={c} shape="land" onSelect={() => nav(`/item/${c.id}`)} onPlay={() => playItem(c.id).catch((e: Error) => toast(e.message))} />)}
          </TvRow>
        )}
      </div>
    </div>
  );
}
