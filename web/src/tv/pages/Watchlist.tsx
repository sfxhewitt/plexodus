import { useCallback, useContext, useEffect, useRef } from "react";
import { useNavigate } from "react-router-dom";
import { art, Card } from "../../api";
import { useApi } from "../../hooks";
import { BackdropCtx, Spinner, TvCard } from "../components";
import { usePageFocus, useRefresh, useToast } from "../hooks";
import { playCard } from "../play";

/** Everything you've added to your Watchlist, unwatched first. */
export default function WatchlistPage() {
  const nav = useNavigate();
  const toast = useToast();
  const { data, error, reload } = useApi<{ items: Card[] }>("/api/watchlist");
  useRefresh(reload);
  usePageFocus(!!data);
  const setBackdrop = useContext(BackdropCtx);
  const timer = useRef<ReturnType<typeof setTimeout>>();
  useEffect(() => () => {
    clearTimeout(timer.current);
    setBackdrop(undefined);
  }, [setBackdrop]);
  const onFocusCard = useCallback((c: Card) => {
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setBackdrop(art(c.backdrop, "w1280")), 350);
  }, [setBackdrop]);
  const open = useCallback((c: Card) => nav(`/item/${c.id}`), [nav]);
  const playIt = useCallback((c: Card) => playCard(c).catch((e: Error) => toast(e.message)), [toast]);

  if (error) return <div className="tv-center"><p className="tv-muted">{error}</p></div>;
  if (!data) return <Spinner />;
  const items = data.items;
  const left = items.filter((c) => !c.watched).length;
  return (
    <div className="tv-page tv-library" data-vscroll="reveal" data-margin="5rem">
      <header className="tv-lib-head" data-row data-row-top>
        <div>
          <h1 className="tv-h1">Watchlist</h1>
          <div className="tv-muted tv-count">{items.length ? `${left} to watch${items.length > left ? ` · ${items.length - left} watched` : ""}` : " "}</div>
        </div>
      </header>
      {items.length === 0 ? (
        <div className="tv-center small">
          <p className="tv-muted">Nothing on your Watchlist yet. Hold OK on any film or show, or open it and choose Watchlist.</p>
        </div>
      ) : (
        <div className="tv-grid">
          {items.map((c, i) => (
            <div key={c.id} data-row>
              <TvCard c={c} shape="poster" autoFocus={i === 0} onSelect={open} onPlay={playIt} onFocusCard={onFocusCard} />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
