import { Link } from "react-router-dom";
import { Card } from "../api";
import { PosterCard } from "../components/Cards";
import { useApi } from "../hooks";

/** Everything you've added to your Watchlist, unwatched first. */
export default function Watchlist() {
  const { data, error } = useApi<{ items: Card[] }>("/api/watchlist");
  if (error) return <div className="page-msg">{error}</div>;
  if (!data) return <div className="page-loading"><div className="spinner" /></div>;
  const items = data.items;
  const left = items.filter((c) => !c.watched).length;
  return (
    <div className="page watchlist-page">
      <h1>Watchlist</h1>
      {items.length === 0 ? (
        <div className="page-msg">
          Nothing on your Watchlist yet. Open any film or show and tap Add to Watchlist. <Link to="/libraries" className="link">Browse your libraries</Link>
        </div>
      ) : (
        <>
          <p className="muted watchlist-intro">{left} to watch{items.length > left ? ` · ${items.length - left} watched` : ""}</p>
          <div className="grid">{items.map((c) => <PosterCard key={c.id} c={c} />)}</div>
        </>
      )}
    </div>
  );
}
