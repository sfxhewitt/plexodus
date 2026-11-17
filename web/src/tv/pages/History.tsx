import { useCallback, useContext, useEffect, useState } from "react";
import { api, Card, episodeLabel, HistoryCard, historyDays, historyNote, type HistoryPage } from "../../api";
import { BackdropCtx, Spinner, TvButton, TvCard, TvRow } from "../components";
import { usePageFocus, useRefresh, useToast } from "../hooks";
import { playItem } from "../play";

const HISTORY_MENU = { history: true };

/**
 * Everything this profile has played, however briefly, newest first and
 * grouped by day. OK picks up where you left off; hold OK for more.
 */
export default function TvHistoryPage() {
  const toast = useToast();
  const [items, setItems] = useState<HistoryCard[] | null>(() => api.cached<HistoryPage>("/api/history")?.items ?? null);
  const [more, setMore] = useState(0);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const reload = useCallback(() => {
    api.get<HistoryPage>("/api/history").then((p) => {
      setItems(p.items);
      setMore(p.more ? p.before : 0);
    }, (e: Error) => setError(e.message));
  }, []);
  useEffect(reload, [reload]);
  useRefresh(reload);
  usePageFocus(!!items);

  const loadMore = useCallback(() => {
    if (!more || loading) return;
    setLoading(true);
    api.get<HistoryPage>(`/api/history?before=${more}`).then((p) => {
      setItems((cur) => [...(cur || []), ...p.items]);
      setMore(p.more ? p.before : 0);
    }, (e: Error) => toast(e.message)).finally(() => setLoading(false));
  }, [more, loading, toast]);

  const setBackdrop = useContext(BackdropCtx);
  // No artwork behind the grid as you move: it stays calm, like the library pages.
  useEffect(() => setBackdrop(undefined), [setBackdrop]);
  // OK resumes (or plays again, if it was finished).
  const play = useCallback((c: Card) => {
    playItem(c.id, { fromStart: !!c.watched && !c.position }).catch((e: Error) => toast(e.message));
  }, [toast]);

  if (error && !items) return <div className="tv-center"><p className="tv-muted">{error}</p></div>;
  if (!items) return <Spinner />;
  return (
    <div className="tv-page tv-history" data-vscroll="reveal" data-margin="5rem">
      <header className="tv-lib-head" data-row data-row-top>
        <div>
          <h1 className="tv-h1">History</h1>
          <div className="tv-muted tv-count">Everything you've played, newest first. OK picks up where you left off.</div>
        </div>
      </header>
      {items.length === 0 ? (
        <div className="tv-center small">
          <p className="tv-muted">Nothing played yet. Anything you start shows up here, even for a few seconds.</p>
        </div>
      ) : (
        historyDays(items).map((d, di) => (
          <TvRow key={d.label} id={`history-${d.label}`} title={d.label}>
            {d.items.map((c, i) => (
              <TvCard key={c.id} c={c} shape="land" autoFocus={di === 0 && i === 0}
                title={c.kind === "episode" ? c.showTitle || c.title : c.title}
                sub={[c.kind === "episode" ? episodeLabel(c) : "", historyNote(c)].filter(Boolean).join(" · ")}
                onSelect={play} onPlay={play} menu={HISTORY_MENU} />
            ))}
          </TvRow>
        ))
      )}
      {!!more && (
        <div className="tv-history-more" data-row>
          <TvButton fk="history-more" onClick={loadMore}>{loading ? "Loading…" : "Show older"}</TvButton>
        </div>
      )}
      <div className="tv-rows-tail" />
    </div>
  );
}
