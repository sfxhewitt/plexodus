import { useCallback, useContext, useEffect, useRef } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { art, Collection, CollectionShelf } from "../../api";
import { Grid } from "../../components/Icons";
import { useApi } from "../../hooks";
import { CollectionTile } from "../collectionTile";
import { BackdropCtx, Spinner, TvRow } from "../components";
import { usePageFocus, useRefresh } from "../hooks";

/** How many tiles a shelf shows before "See all". */
const SHELF_MAX = 30;

type Data = { groups: CollectionShelf[]; count: number; pending: number };

/**
 * Every collection: TMDB franchises and all of Plex's collections, as one
 * row per group (franchises, studios, awards, decades…), with collections
 * you're part-way through first. ?group= shows one group as a grid.
 */
export default function CollectionsPage() {
  const nav = useNavigate();
  const [params] = useSearchParams();
  const group = params.get("group");
  const { data, error, reload } = useApi<Data>("/api/collections");
  useRefresh(reload);
  usePageFocus(!!data);
  const setBackdrop = useContext(BackdropCtx);
  const timer = useRef<ReturnType<typeof setTimeout>>();
  useEffect(() => () => {
    clearTimeout(timer.current);
    setBackdrop(undefined);
  }, [setBackdrop]);
  const onFocusTile = useCallback((c: Collection) => {
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setBackdrop(art(c.backdrop, "w1280")), 350);
  }, [setBackdrop]);
  const open = useCallback((c: Collection) => nav(`/collection/${c.id}`), [nav]);

  if (error) return <div className="tv-center"><p className="tv-muted">{error}</p></div>;
  if (!data) return <Spinner />;
  const shelves = data.groups;
  const shelf = group ? shelves.find((s) => s.name === group) : undefined;

  if (group || shelves.length <= 1) {
    const list = shelf?.collections ?? (group ? [] : shelves.flatMap((s) => s.collections));
    return (
      <div className="tv-page tv-library" data-vscroll="reveal" data-margin="5rem">
        <header className="tv-lib-head" data-row data-row-top>
          <div>
            <h1 className="tv-h1">{group || "Collections"}</h1>
            <div className="tv-muted tv-count">
              {list.length.toLocaleString()} collections
              {!group && data.pending > 0 ? ` · still sorting ${data.pending.toLocaleString()} films` : ""}
            </div>
          </div>
        </header>
        {list.length === 0 ? <Empty pending={data.pending} /> : (
          <div className="tv-grid">
            {list.map((c, i) => (
              <div key={c.id} data-row>
                <CollectionTile c={c} autoFocus={i === 0} onSelect={open} onFocusTile={onFocusTile} />
              </div>
            ))}
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="tv-page tv-library tv-collections" data-vscroll="reveal" data-margin="5rem">
      <header className="tv-lib-head" data-row data-row-top>
        <div>
          <h1 className="tv-h1">Collections</h1>
          <div className="tv-muted tv-count">
            {data.count.toLocaleString()} collections
            {data.pending > 0 ? ` · still sorting ${data.pending.toLocaleString()} films` : ""}
          </div>
        </div>
      </header>
      {shelves.map((s, si) => (
        <TvRow key={s.name} id={`shelf-${si}`} title={`${s.name} · ${s.collections.length.toLocaleString()}`}>
          {s.collections.slice(0, SHELF_MAX).map((c, i) => (
            <CollectionTile key={c.id} c={c} autoFocus={si === 0 && i === 0} onSelect={open} onFocusTile={onFocusTile} />
          ))}
          {s.collections.length > SHELF_MAX && (
            <button className="tv-card poster tv-see-all" data-f data-fk={`all-${si}`}
              onClick={() => nav(`/collection?group=${encodeURIComponent(s.name)}`)}>
              <div className="tv-card-frame">
                <Grid size={40} />
                <span>See all {s.collections.length.toLocaleString()}</span>
              </div>
            </button>
          )}
        </TvRow>
      ))}
      <div className="tv-page-tail" />
    </div>
  );
}

function Empty({ pending }: { pending: number }) {
  return (
    <div className="tv-center small">
      <p className="tv-muted">
        {pending > 0
          ? "Plexodus is working out which films belong together. Collections appear here as it goes."
          : "No collections yet. Your Plex collections, and films that belong together like a trilogy, show up here."}
      </p>
    </div>
  );
}
