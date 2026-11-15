import { useCallback, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { art, Card } from "../../api";
import { useApi } from "../../hooks";
import { Spinner, TvButton, TvCard, TvRow, useBackdrop } from "../components";
import { usePageFocus, useToast } from "../hooks";
import { playCard } from "../play";
import { Credit, initials, PersonPageData, personFacts } from "../../people";

/** A tile for a title you don't have: TMDB's poster, not playable. */
const creditCard = (c: Credit): Card => ({
  id: -(c.tmdbId * 2 + (c.kind === "show" ? 1 : 0)), kind: c.kind, title: c.title, year: c.year, poster: c.poster,
} as Card);

/** A cast member: who they are, what of theirs you can play, and the rest of their work. */
export default function PersonPage() {
  const { id } = useParams();
  const nav = useNavigate();
  const toast = useToast();
  const { data, error } = useApi<PersonPageData>(`/api/people/${id}`);
  const [more, setMore] = useState(false);
  const ready = !!data && String(data.person.id) === id;
  const lead = ready ? data.inLibrary.find((c) => c.backdrop) : undefined;
  useBackdrop(art(lead?.backdrop, "w1280"));
  usePageFocus(ready);
  const open = useCallback((c: Card) => nav(`/item/${c.id}`), [nav]);
  const playIt = useCallback((c: Card) => playCard(c).catch((e: Error) => toast(e.message)), [toast]);
  const notHere = useCallback((c: Card) => toast(`${c.title}${c.year ? ` (${c.year})` : ""} isn't in your library`), [toast]);

  if (error) return <div className="tv-center"><p className="tv-muted">{error}</p></div>;
  if (!ready) return <Spinner />;
  const { person: p, inLibrary, roles, elsewhere } = data;
  const facts = personFacts(p);
  const longBio = (p.biography?.length || 0) > 420;
  const role = (c: Card) => roles[String(c.id)] || (c.year ? String(c.year) : "");
  return (
    <div className="tv-page tv-detail tv-person-page" data-vscroll="reveal" data-margin="4rem">
      <section className="tv-detail-hero tv-person-hero" data-row data-row-top>
        <div className="tv-person-portrait">
          {p.profile ? <img src={art(p.profile, "w500")} alt="" /> : <span>{initials(p.name)}</span>}
        </div>
        <div className="tv-detail-info">
          {p.knownFor && <div className="tv-eyebrow">{p.knownFor === "Acting" ? "Actor" : p.knownFor}</div>}
          <h1 className="tv-detail-title">{p.name}</h1>
          {facts.length > 0 && <div className="tv-meta">{facts.map((f) => <span key={f}>{f}</span>)}</div>}
          {p.biography && <p className={`tv-overview ${more ? "full" : ""}`}>{p.biography}</p>}
          <div className="tv-actions" data-group="actions">
            {inLibrary[0] && (
              <TvButton primary autoFocus fk="first" onClick={() => open(inLibrary[0])}>
                {inLibrary.length === 1 ? `${inLibrary[0].title} is in your library` : `${inLibrary.length} titles in your library`}
              </TvButton>
            )}
            {longBio && <TvButton fk="bio" onClick={() => setMore((m) => !m)}>{more ? "Less" : "Full biography"}</TvButton>}
          </div>
          {!inLibrary.length && !longBio && <p className="tv-muted tv-person-none">Nothing with {p.name} in your libraries yet.</p>}
        </div>
      </section>

      {inLibrary.length > 0 && (
        <TvRow id={`person-lib-${p.id}`} title="In your library">
          {inLibrary.map((c, i) => (
            <TvCard key={c.id} c={c} shape="poster" sub={role(c)} onSelect={open} onPlay={playIt} isDefault={i === 0} />
          ))}
        </TvRow>
      )}
      {elsewhere.length > 0 && (
        <TvRow id={`person-more-${p.id}`} title={inLibrary.length ? "More from their career" : "Known for"}>
          {elsewhere.map((c, i) => {
            const card = creditCard(c);
            return <TvCard key={card.id} c={card} shape="poster" menu={false} sub={c.character || (c.year ? String(c.year) : "")} onSelect={notHere} isDefault={i === 0} />;
          })}
        </TvRow>
      )}
      <div className="tv-page-tail" />
    </div>
  );
}
