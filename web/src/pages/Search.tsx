import { useSearchParams } from "react-router-dom";
import { Card } from "../api";
import { LandscapeCard, PosterCard } from "../components/Cards";
import { useApi, useDebounced } from "../hooks";

type Results = { movies: Card[]; shows: Card[]; episodes: Card[] };

export default function Search() {
  const [params] = useSearchParams();
  const q = useDebounced(params.get("q") || "", 150);
  const { data } = useApi<Results>(q ? `/api/search?q=${encodeURIComponent(q)}` : null);
  const empty = data && !data.movies.length && !data.shows.length && !data.episodes.length;

  return (
    <div className="search page">
      <h1 className="search-head">{q ? <>Results for <span className="grad">“{q}”</span></> : "Search"}</h1>
      {empty && <div className="page-msg">Nothing matches “{q}”.</div>}
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
