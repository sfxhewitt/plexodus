import { useSearchParams } from "react-router-dom";
import { AskResult, Card, looksLikeQuestion } from "../api";
import { LandscapeCard, PosterCard } from "../components/Cards";
import { Sparkle } from "../components/Icons";
import { useApi, useDebounced } from "../hooks";

type Results = { movies: Card[]; shows: Card[]; episodes: Card[] };

export default function Search() {
  const [params, setParams] = useSearchParams();
  const q = useDebounced(params.get("q") || "", 150);
  const asking = params.get("ask") === "1" && !!q.trim();
  const { data } = useApi<Results>(q ? `/api/search?q=${encodeURIComponent(q)}` : null);
  const titleHits = data && data.movies.length + data.shows.length + data.episodes.length;
  const empty = data && !titleHits && !asking;
  const ask = () => setParams({ q, ask: "1" }, { replace: true });

  return (
    <div className="search page">
      {!asking && <h1 className="search-head">{q ? <>Results for <span className="grad">“{q}”</span></> : "Search"}</h1>}
      {asking && <Ask q={q} />}
      {!asking && q && (looksLikeQuestion(q) || data?.movies.length === 0) && (
        <button className="ask-offer" onClick={ask}>
          <Sparkle /> <span>Ask in plain English: <b>“{q}”</b></span><span className="muted small">Press Enter</span>
        </button>
      )}
      {empty && <div className="page-msg">No titles match “{q}”.</div>}
      {asking && !!titleHits && <h2 className="ask-sub">Titles matching “{q}”</h2>}
      {data && data.movies.length > 0 && (
        <section>
          <h2 className="section-title">Movies</h2>
          <div className="grid">{data.movies.map((c) => <PosterCard key={c.id} c={c} />)}</div>
        </section>
      )}
      {data && data.shows.length > 0 && (
        <section>
          <h2 className="section-title">Shows</h2>
          <div className="grid">{data.shows.map((c) => <PosterCard key={c.id} c={c} />)}</div>
        </section>
      )}
      {data && data.episodes.length > 0 && (
        <section>
          <h2 className="section-title">Episodes</h2>
          <div className="land-grid">{data.episodes.map((c) => <LandscapeCard key={c.id} c={c} to={`/item/${c.id}`} />)}</div>
        </section>
      )}
    </div>
  );
}

function Ask({ q }: { q: string }) {
  const { data, error } = useApi<AskResult>(`/api/ask?q=${encodeURIComponent(q)}`);
  return (
    <div className="ask">
      <div className="ask-q"><Sparkle size={16} /> “{q}”</div>
      {!data && !error && <div className="ask-thinking"><span className="spinner sm" /> Finding titles that fit…</div>}
      {error && <div className="page-msg">{error}</div>}
      {data && (
        <>
          <h1 className="search-head grad">{data.summary}</h1>
          {data.notice && <div className="ask-notice">{data.notice}</div>}
          {data.engine === "basic" && !data.notice && (
            <div className="muted small ask-engine">Basic mode: understands decades, lengths, genres and “haven’t seen”. Add an Anthropic API key in Settings for smarter answers and “something like…” recommendations.</div>
          )}
          {!data.picks.length && !data.matches.length && <div className="page-msg">Nothing in your library fits that. Try saying it another way.</div>}
          {data.picks.length > 0 && (
            <section>
              <h2 className="section-title">Best fits</h2>
              <div className="grid">{data.picks.map((c) => <PosterCard key={c.id} c={c} />)}</div>
            </section>
          )}
          {data.matches.length > 0 && (
            <section>
              <h2 className="section-title">{data.picks.length ? "More that match" : "Matches"}</h2>
              <div className="grid">{data.matches.map((c) => <PosterCard key={c.id} c={c} />)}</div>
            </section>
          )}
        </>
      )}
    </div>
  );
}
