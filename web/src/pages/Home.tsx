import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { art, Card, fmtRuntime, Library, Row as RowT } from "../api";
import { Row } from "../components/Cards";
import { Info, Play } from "../components/Icons";
import { useApi, useUser } from "../hooks";

function Hero({ items }: { items: Card[] }) {
  const [i, setI] = useState(0);
  useEffect(() => {
    if (items.length < 2) return;
    const t = setInterval(() => setI((n) => (n + 1) % items.length), 9000);
    return () => clearInterval(t);
  }, [items.length, i]);
  if (!items.length) return null;
  const c = items[i];
  return (
    <section className="hero">
      {items.map((h, n) => (
        <div key={h.id} className={`hero-bg ${n === i ? "on" : ""}`}
          style={{ backgroundImage: `url(${art(h.backdrop, window.innerWidth < 900 ? "w780" : "w1280")})` }} />
      ))}
      <div className="hero-shade" />
      <div className="hero-content" key={c.id}>
        {c.logo ? (
          <img className="hero-logo" src={art(c.logo, "w500")} alt={c.title} />
        ) : (
          <h1 className="hero-title">{c.title}</h1>
        )}
        <div className="hero-meta">
          {c.rating ? <span className="pill">★ {c.rating.toFixed(1)}</span> : null}
          {c.year ? <span>{c.year}</span> : null}
          {c.runtime ? <span>{fmtRuntime(c.runtime)}</span> : null}
          <span className="cap">{c.kind === "show" ? "Series" : "Film"}</span>
        </div>
        {c.overview && <p className="hero-overview">{c.overview}</p>}
        <div className="hero-actions">
          <Link to={c.kind === "movie" ? `/play/${c.id}` : `/item/${c.id}`} className="btn primary"><Play size={18} /> Play</Link>
          <Link to={`/item/${c.id}`} className="btn glass"><Info size={18} /> More info</Link>
        </div>
      </div>
      <div className="hero-dots">
        {items.map((h, n) => (
          <button key={h.id} className={n === i ? "on" : ""} onClick={() => setI(n)} aria-label={`Show ${h.title}`} />
        ))}
      </div>
    </section>
  );
}

export default function Home() {
  const { data, error } = useApi<{ hero: Card[]; rows: RowT[] }>("/api/home");
  const { data: libs } = useApi<Library[]>("/api/libraries");
  const { user } = useUser();

  if (error) return <div className="page-msg">{error}</div>;
  if (!data) return <div className="page-loading"><div className="spinner" /></div>;

  if (libs && libs.length === 0) {
    return (
      <div className="empty-state">
        <div className="empty-glow" />
        <h1>Welcome to Plexodus</h1>
        <p>Point it at your media folders and it will index them in the background.</p>
        {user?.isAdmin ? <Link to="/settings" className="btn primary">Add a library</Link> : <p>Ask an admin to add a library.</p>}
      </div>
    );
  }

  return (
    <div className={`home ${data.hero.length ? "" : "no-hero"}`}>
      <Hero items={data.hero} />
      <div className="rows">
        {data.rows.map((r) => <Row key={r.title} {...r} />)}
      </div>
    </div>
  );
}
