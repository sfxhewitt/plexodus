import { memo } from "react";
import { art, Collection } from "../api";
import { Check } from "../components/Icons";
import { Art } from "./components";

/** A collection as a poster tile, with how many of its films you've seen. */
export const CollectionTile = memo(function CollectionTile({ c, onSelect, onFocusTile, autoFocus }: {
  c: Collection; onSelect: (c: Collection) => void; onFocusTile?: (c: Collection) => void; autoFocus?: boolean;
}) {
  const done = c.count > 0 && c.watched >= c.count;
  return (
    <button className="tv-card poster tv-collection-tile" data-f data-fk={`col${c.id}`} data-autofocus={autoFocus ? "" : undefined}
      onClick={() => onSelect(c)} onFocus={() => onFocusTile?.(c)}>
      <div className="tv-card-frame">
        <Art src={art(c.poster, "w342")} title={c.name} />
        <span className="tv-card-label">{c.count} films</span>
        {done && <span className="tv-badge done"><Check size={14} /></span>}
      </div>
      <div className="tv-card-cap">
        <div className="t">{c.name.replace(/ Collection$/, "")}</div>
        <div className="s">{done ? "All watched" : c.watched ? `${c.watched} of ${c.count} watched` : `${c.count} films`}</div>
      </div>
    </button>
  );
});
