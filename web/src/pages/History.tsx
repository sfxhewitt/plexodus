import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, art, episodeLabel, HistoryCard, historyDays, historyNote, type HistoryPage } from "../api";
import { Img } from "../components/Cards";
import { Play, X } from "../components/Icons";

/** Everything this profile has played, however briefly, newest first, to get back to. */
export default function History() {
  const [items, setItems] = useState<HistoryCard[] | null>(() => api.cached<HistoryPage>("/api/history")?.items ?? null);
  const [more, setMore] = useState<number>(0);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    api.get<HistoryPage>("/api/history").then((p) => {
      setItems(p.items);
      setMore(p.more ? p.before : 0);
    }, (e: Error) => setError(e.message));
  }, []);

  const loadMore = useCallback(() => {
    if (!more || loading) return;
    setLoading(true);
    api.get<HistoryPage>(`/api/history?before=${more}`).then((p) => {
      setItems((cur) => [...(cur || []), ...p.items]);
      setMore(p.more ? p.before : 0);
    }, (e: Error) => setError(e.message)).finally(() => setLoading(false));
  }, [more, loading]);

  const remove = async (c: HistoryCard) => {
    setItems((cur) => cur?.filter((x) => x.id !== c.id) ?? null);
    api.invalidate("/api/history");
    await api.del(`/api/history/${c.id}`).catch((e: Error) => setError(e.message));
  };

  if (error && !items) return <div className="page-msg">{error}</div>;
  if (!items) return <div className="page-loading"><div className="spinner" /></div>;
  return (
    <div className="page history-page">
      <h1>History</h1>
      <p className="muted history-intro">Everything you've played, however briefly, newest first. Pick up where you left off.</p>
      {error && <div className="err">{error}</div>}
      {items.length === 0 && <div className="page-msg">Nothing played yet. Anything you start shows up here, even for a few seconds.</div>}
      {historyDays(items).map((d) => (
        <section key={d.label} className="history-day">
          <h2>{d.label}</h2>
          <div className="history-list">
            {d.items.map((c) => <HistoryRow key={c.id} c={c} onRemove={remove} />)}
          </div>
        </section>
      ))}
      {!!more && (
        <div className="history-more">
          <button className="btn glass" disabled={loading} onClick={loadMore}>{loading ? "Loading…" : "Show older"}</button>
        </div>
      )}
    </div>
  );
}

function HistoryRow({ c, onRemove }: { c: HistoryCard; onRemove: (c: HistoryCard) => void }) {
  const isEp = c.kind === "episode";
  const title = isEp ? c.showTitle || c.title : c.title;
  const sub = isEp ? [episodeLabel(c), c.title].filter(Boolean).join(" · ") : c.year ? String(c.year) : "";
  const resume = !!c.position && !c.watched;
  const pct = c.position && c.duration ? Math.min(100, (c.position / c.duration) * 100) : c.watched ? 100 : 0;
  return (
    <div className="history-row">
      <Link to={`/item/${c.id}`} className="history-thumb">
        <Img src={art(c.backdrop || c.showBackdrop || c.poster, "w500")} alt={title} title={title} />
        {pct > 0 && <div className="progress"><div style={{ width: `${pct}%` }} /></div>}
      </Link>
      <Link to={`/item/${c.id}`} className="history-text">
        <div className="t">{title}</div>
        {sub && <div className="s">{sub}</div>}
        <div className="muted small">{historyNote(c)}</div>
      </Link>
      <div className="history-actions">
        <Link to={`/play/${c.id}${resume ? "" : "?from=0"}`} className="btn sm primary">
          <Play size={14} /><span>{resume ? "Resume" : "Play"}</span>
        </Link>
        <button className="icon-btn" aria-label={`Remove ${title} from History`} title="Remove from History" onClick={() => onRemove(c)}>
          <X size={18} />
        </button>
      </div>
    </div>
  );
}
