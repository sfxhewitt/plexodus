import { useCallback, useContext, useEffect, useRef } from "react";
import { useNavigate } from "react-router-dom";
import { art, Collection } from "../../api";
import { useApi } from "../../hooks";
import { CollectionTile } from "../collectionTile";
import { BackdropCtx, Spinner } from "../components";
import { usePageFocus, useRefresh } from "../hooks";

/** Every film collection (franchise) with two or more films in your libraries. */
export default function CollectionsPage() {
  const nav = useNavigate();
  const { data, error, reload } = useApi<{ collections: Collection[]; pending: number }>("/api/collections");
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
  const list = data.collections;
  return (
    <div className="tv-page tv-library" data-vscroll="reveal" data-margin="5rem">
      <header className="tv-lib-head" data-row data-row-top>
        <div>
          <h1 className="tv-h1">Collections</h1>
          <div className="tv-muted tv-count">
            {list.length.toLocaleString()} collections
            {data.pending > 0 ? ` · still sorting ${data.pending.toLocaleString()} films` : ""}
          </div>
        </div>
      </header>
      {list.length === 0 ? (
        <div className="tv-center small">
          <p className="tv-muted">
            {data.pending > 0
              ? "Plexodus is working out which films belong together. Collections appear here as it goes."
              : "No collections yet. Films that belong together, like a trilogy, show up here."}
          </p>
        </div>
      ) : (
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
