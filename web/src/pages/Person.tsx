import { useState } from "react";
import { useParams } from "react-router-dom";
import { art } from "../api";
import { Img, Row } from "../components/Cards";
import { useApi } from "../hooks";
import { initials, PersonPageData, personFacts } from "../people";

/** A cast member: who they are, what of theirs is in your libraries, then the rest of their work. */
export default function Person() {
  const { id } = useParams();
  const { data, error } = useApi<PersonPageData>(`/api/people/${id}`);
  const [more, setMore] = useState(false);
  if (error) return <div className="page-msg">{error}</div>;
  if (!data || String(data.person.id) !== id) return <div className="page-loading"><div className="spinner" /></div>;
  const { person: p, inLibrary, roles, elsewhere } = data;
  const backdrop = inLibrary.find((c) => c.backdrop)?.backdrop;
  const facts = personFacts(p);
  const items = inLibrary.map((c) => ({ ...c, note: roles[String(c.id)] || (c.year ? String(c.year) : "") }));
  return (
    <div className="detail person">
      <div className="detail-bg" style={backdrop ? { backgroundImage: `url(${art(backdrop, "w1280")})` } : undefined} />
      <div className="detail-shade" />
      <div className="detail-main page">
        <div className="detail-poster">
          {p.profile ? <Img src={art(p.profile, "w500")} alt={p.name} title={p.name} /> : <div className="img person-initials"><span>{initials(p.name)}</span></div>}
        </div>
        <div className="detail-info">
          {p.knownFor && <div className="crumb">{p.knownFor === "Acting" ? "Actor" : p.knownFor}</div>}
          <h1>{p.name}</h1>
          {facts.length > 0 && <div className="meta-line">{facts.map((f) => <span key={f}>{f}</span>)}</div>}
          {p.biography && (
            <>
              <p className={`overview person-bio ${more ? "full" : ""}`}>{p.biography}</p>
              {p.biography.length > 500 && <button className="btn glass sm" onClick={() => setMore((m) => !m)}>{more ? "Less" : "Read more"}</button>}
            </>
          )}
          {!inLibrary.length && <p className="muted" style={{ marginTop: 16 }}>Nothing with {p.name} in your libraries yet.</p>}
        </div>
      </div>
      <div className="detail-below page">
        {items.length > 0 && <Row title="In your library" kind="poster" items={items} />}
        {elsewhere.length > 0 && (
          <section className="row row-poster">
            <div className="row-head"><h2 className="row-title">{inLibrary.length ? "More from their career" : "Known for"}</h2></div>
            <div className="row-track">
              {elsewhere.map((c) => (
                <div key={c.kind + c.tmdbId} className="poster-card elsewhere" title={`${c.title} isn't in your library`}>
                  <div className="poster-frame"><Img src={art(c.poster, "w342")} alt={c.title} title={c.title} /></div>
                  <div className="poster-meta">
                    <div className="t">{c.title}</div>
                    <div className="s">{[c.year, c.character].filter(Boolean).join(" · ")}</div>
                  </div>
                </div>
              ))}
            </div>
          </section>
        )}
      </div>
    </div>
  );
}
