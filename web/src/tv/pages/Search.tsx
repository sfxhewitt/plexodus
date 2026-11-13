import { useEffect, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { AskResult, Card, Collection } from "../../api";
import { Search as SearchIcon, Sparkle } from "../../components/Icons";
import { useApi, useDebounced } from "../../hooks";
import { TvCard, TvRow, useBackdrop } from "../components";
import { focusEl, focusKey } from "../focus";
import { usePageFocus, useToast } from "../hooks";
import { playItem } from "../play";
import { CollectionTile } from "../collectionTile";

type Results = { movies: Card[]; shows: Card[]; episodes: Card[]; collections?: Collection[] };
const KEYS = "abcdefghijklmnopqrstuvwxyz1234567890".split("");
const EXAMPLES = ["90s heist films under two hours I haven't seen", "Something like Severance", "Funny shows I haven't started", "Scary films from the 80s"];

export default function SearchPage() {
  const [params, setParams] = useSearchParams();
  const nav = useNavigate();
  const toast = useToast();
  const [q, setQ] = useState(() => params.get("q") || "");
  // The request being asked in plain English (Enter, the Ask key or an example).
  const [asked, setAsked] = useState(() => (params.get("ask") === "1" ? (params.get("q") || "").trim() : ""));
  const dq = useDebounced(q.trim(), 250);
  const asking = !!asked && asked === dq;
  const { data } = useApi<Results>(dq && !asking ? `/api/search?q=${encodeURIComponent(dq)}` : null);
  const { data: answer, error: askError } = useApi<AskResult>(asking ? `/api/ask?q=${encodeURIComponent(asked)}` : null);
  const ask = (text: string) => {
    const t = text.trim();
    if (!t) return;
    setQ(t);
    setAsked(t);
    // The examples go away while Claude thinks; park focus on the Ask key.
    if (document.activeElement instanceof HTMLElement && document.activeElement.dataset.fk?.startsWith("ex-")) focusKey("k-ask");
  };

  // When an answer arrives, move to it (unless the viewer has started typing).
  useEffect(() => {
    if (!answer) return;
    const a = document.activeElement as HTMLElement | null;
    const fk = a?.dataset.fk || "";
    if (a && a.isConnected && (fk.startsWith("k-") && fk !== "k-ask")) return;
    focusEl(document.querySelector<HTMLElement>(".tv-search-results [data-f]"));
  }, [answer]);
  useBackdrop(undefined);
  usePageFocus(true);

  // Keep the query in the URL so Back from a result comes back to it.
  useEffect(() => {
    const want = asking ? "1" : null;
    if ((params.get("q") || "") !== dq || params.get("ask") !== want) setParams(dq ? (asking ? { q: dq, ask: "1" } : { q: dq }) : {}, { replace: true, state: { focus: document.activeElement instanceof HTMLElement ? document.activeElement.dataset.fk : undefined } });
  }, [dq, asking]);

  const type = (k: string) => setQ((v) => (v + k).slice(0, 60));
  const results = data && dq && !asking ? data : undefined;
  const empty = results && !results.movies.length && !results.shows.length && !results.episodes.length && !results.collections?.length;

  return (
    <div className="tv-search">
      <div className="tv-kb" data-group="kb" data-remember="true">
        <label className="tv-search-input">
          <SearchIcon size={26} />
          <input data-f data-fk="input" value={q} placeholder="Search or ask" enterKeyHint="search" onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); ask(q); } }} />
        </label>
        <div className="tv-keys">
          {KEYS.map((k, i) => (
            <button key={k} data-f data-fk={`k-${k}`} data-autofocus={i === 0 ? "" : undefined} className="tv-key" onClick={() => type(k)}>{k}</button>
          ))}
          <button data-f data-fk="k-space" className="tv-key wide" onClick={() => type(" ")}>Space</button>
          <button data-f data-fk="k-del" className="tv-key wide" onClick={() => setQ((v) => v.slice(0, -1))}>Delete</button>
          <button data-f data-fk="k-clear" className="tv-key wide" onClick={() => (setQ(""), setAsked(""))}>Clear</button>
          <button data-f data-fk="k-ask" className={`tv-key ask ${q.trim() ? "" : "dim"}`} onClick={() => ask(q)}><Sparkle size={22} /> Ask in plain English</button>
        </div>
      </div>
      <div className="tv-search-results" data-vscroll="reveal" data-margin="3rem">
        {!dq && (
          <div className="tv-search-hint">
            <h1 className="tv-h1">Search</h1>
            <p className="tv-muted">Type a title with the keys on the left, or select the box to use your remote's keyboard or voice search. You can also ask for what you're in the mood for:</p>
            <div className="tv-ask-examples" data-group="kb">
              {EXAMPLES.map((x, i) => (
                <button key={x} data-f data-fk={`ex-${i}`} className="tv-ask-example" onClick={() => ask(x)}><Sparkle size={20} /> {x}</button>
              ))}
            </div>
          </div>
        )}
        {asking && !answer && !askError && <div className="tv-search-hint"><p className="tv-ask-q"><Sparkle size={22} /> “{asked}”</p><p className="tv-muted">Finding titles that fit…</p></div>}
        {asking && askError && <div className="tv-search-hint"><p className="tv-muted">{askError}</p></div>}
        {asking && answer && (
          <>
            <div className="tv-search-hint">
              <p className="tv-ask-q"><Sparkle size={22} /> “{asked}”</p>
              <h1 className="tv-h1 tv-ask-title">{answer.summary}</h1>
              {answer.notice && <p className="tv-muted">{answer.notice}</p>}
              {!answer.picks.length && !answer.matches.length && <p className="tv-muted">Nothing in your library fits that. Try saying it another way.</p>}
            </div>
            {answer.picks.length > 0 && (
              <TvRow id="ask-picks" title="Best fits">
                {answer.picks.map((c) => <TvCard key={c.id} c={c} shape="poster" onSelect={() => nav(`/item/${c.id}`)} onPlay={c.kind === "movie" ? () => playItem(c.id).catch((e: Error) => toast(e.message)) : undefined} />)}
              </TvRow>
            )}
            {answer.matches.length > 0 && (
              <TvRow id="ask-matches" title={answer.picks.length ? "More that match" : "Matches"}>
                {answer.matches.map((c) => <TvCard key={c.id} c={c} shape="poster" onSelect={() => nav(`/item/${c.id}`)} onPlay={c.kind === "movie" ? () => playItem(c.id).catch((e: Error) => toast(e.message)) : undefined} />)}
              </TvRow>
            )}
          </>
        )}
        {empty && <div className="tv-search-hint"><p className="tv-muted">Nothing matches “{dq}”.</p></div>}
        {results && results.movies.length > 0 && (
          <TvRow id="sr-movies" title="Movies">
            {results.movies.map((c) => <TvCard key={c.id} c={c} shape="poster" onSelect={() => nav(`/item/${c.id}`)} onPlay={() => playItem(c.id).catch((e: Error) => toast(e.message))} />)}
          </TvRow>
        )}
        {results && results.collections && results.collections.length > 0 && (
          <TvRow id="sr-collections" title="Collections">
            {results.collections.map((c) => <CollectionTile key={c.id} c={c} onSelect={() => nav(`/collection/${c.id}`)} />)}
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
