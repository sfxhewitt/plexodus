import { useCallback } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { art, Card, Collection } from "../../api";
import { Play } from "../../components/Icons";
import { useApi } from "../../hooks";
import { Spinner, TvButton, TvCard, useBackdrop } from "../components";
import { usePageFocus, useRefresh, useToast } from "../hooks";
import { playCard } from "../play";

/** One collection: its films in release order, with Play for the next one you haven't seen. */
export default function CollectionPage() {
  const { id } = useParams();
  const nav = useNavigate();
  const toast = useToast();
  const { data, error, reload } = useApi<{ collection: Collection; items: Card[] }>(`/api/collections/${id}`);
  useRefresh(reload);
  const ready = !!data && String(data.collection.id) === id;
  useBackdrop(ready ? art(data.collection.backdrop, "w1280") : undefined);
  usePageFocus(ready);
  const open = useCallback((c: Card) => nav(`/item/${c.id}`), [nav]);
  const playIt = useCallback((c: Card) => playCard(c).catch((e: Error) => toast(e.message)), [toast]);

  if (error) return <div className="tv-center"><p className="tv-muted">{error}</p></div>;
  if (!ready) return <Spinner />;
  const { collection: c, items } = data;
  const next = items.find((f) => f.position && !f.watched) || items.find((f) => !f.watched) || items[0];
  const years = items.map((f) => f.year).filter(Boolean) as number[];
  const span = years.length ? (Math.min(...years) === Math.max(...years) ? `${years[0]}` : `${Math.min(...years)}–${Math.max(...years)}`) : "";
  return (
    <div className="tv-page tv-detail" data-vscroll="reveal" data-margin="4rem">
      <section className="tv-detail-hero tv-collection-hero" data-row data-row-top>
        <div className="tv-detail-info">
          <div className="tv-eyebrow">Collection</div>
          <h1 className="tv-detail-title">{c.name}</h1>
          <div className="tv-meta">
            <span>{items.length} films</span>
            {span && <span>{span}</span>}
            {c.watched > 0 && <span>{c.watched >= c.count ? "All watched" : `${c.watched} of ${c.count} watched`}</span>}
          </div>
          <div className="tv-actions" data-group="actions">
            {next && (
              <TvButton primary autoFocus fk="play" icon={<Play size={24} />} onClick={() => playIt(next)}>
                {next.position && !next.watched ? "Resume" : "Play"} {next.title}
              </TvButton>
            )}
          </div>
        </div>
      </section>
      <div className="tv-grid">
        {items.map((f) => (
          <div key={f.id} data-row>
            <TvCard c={f} shape="poster" onSelect={open} onPlay={playIt} isDefault={f.id === next?.id} />
          </div>
        ))}
      </div>
      <div className="tv-page-tail" />
    </div>
  );
}
