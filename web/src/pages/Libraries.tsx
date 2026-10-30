import { Link } from "react-router-dom";
import { art, fmtSize, Library } from "../api";
import { useApi } from "../hooks";

function hue(s: string) {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) % 360;
  return h;
}

export default function Libraries() {
  const { data: libs } = useApi<Library[]>("/api/libraries");
  if (!libs) return <div className="page-loading"><div className="spinner" /></div>;
  return (
    <div className="libraries page">
      <h1>Libraries</h1>
      <div className="lib-tiles">
        {libs.map((l) => (
          <Link key={l.id} to={`/library/${l.id}`} className="lib-tile" style={{ "--h": hue(l.name) } as React.CSSProperties}>
            {l.backdrop && <div className="lib-tile-bg" style={{ backgroundImage: `url(${art(l.backdrop, "w780")})` }} />}
            <div className="lib-tile-body">
              <div className="t">{l.name}</div>
              <div className="s">
                {l.count.toLocaleString()} {l.kind === "movies" ? "movies" : "shows"}
                {l.totalSize ? ` · ${fmtSize(l.totalSize)}` : ""}
              </div>
            </div>
          </Link>
        ))}
      </div>
    </div>
  );
}
