import { Link } from "react-router-dom";
import { art, fmtSize, Library, LibraryPrefs, setLibraryPrefs } from "../api";
import { useApi } from "../hooks";

function hue(s: string) {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) % 360;
  return h;
}

export default function Libraries() {
  const { data: libs, setData } = useApi<Library[]>("/api/libraries");
  const change = (l: Library, prefs: LibraryPrefs) => {
    setData(libs?.map((x) => (x.id === l.id ? { ...x, ...prefs } : x)));
    setLibraryPrefs(l.id, prefs).catch((e: Error) => alert(e.message));
  };
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
      <h2 className="lib-prefs-title">Show in menu and on Home</h2>
      <p className="muted">Only you see these choices. Hidden libraries stay searchable and open from here.</p>
      <div className="lib-prefs">
        {libs.map((l) => (
          <div key={l.id} className="lib-pref">
            <span className="t">{l.name}</span>
            <label><input type="checkbox" checked={!l.hidden} onChange={() => change(l, { hidden: !l.hidden })} /> Menu</label>
            <label><input type="checkbox" checked={!l.hiddenHome} onChange={() => change(l, { hiddenHome: !l.hiddenHome })} /> Home</label>
          </div>
        ))}
      </div>
    </div>
  );
}
