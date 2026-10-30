import { useEffect, useRef, useState } from "react";
import { Link, NavLink, useLocation, useNavigate } from "react-router-dom";
import { api, Library } from "../api";
import { useApi, useUser } from "../hooks";
import { Film, Grid, Home, Logout, Search, Settings, Tv } from "./Icons";

export function Logo() {
  return (
    <Link to="/" className="logo" aria-label="Plexodus home">
      <img src="/favicon.svg" alt="" width={28} height={28} />
      <span>plex<b>odus</b></span>
    </Link>
  );
}

export function Nav() {
  const { user, setUser } = useUser();
  const { data: libs } = useApi<Library[]>("/api/libraries");
  const nav = useNavigate();
  const loc = useLocation();
  const [scrolled, setScrolled] = useState(false);
  const [q, setQ] = useState(() => new URLSearchParams(loc.search).get("q") || "");
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const on = () => setScrolled(window.scrollY > 8);
    on();
    window.addEventListener("scroll", on, { passive: true });
    return () => window.removeEventListener("scroll", on);
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement;
      if ((e.key === "/" || (e.key === "k" && (e.metaKey || e.ctrlKey))) && t.tagName !== "INPUT" && t.tagName !== "TEXTAREA") {
        e.preventDefault();
        input.current?.focus();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => {
    if (!loc.pathname.startsWith("/search")) setQ("");
  }, [loc.pathname]);

  const onSearch = (v: string) => {
    setQ(v);
    const target = v ? `/search?q=${encodeURIComponent(v)}` : "/";
    nav(target, { replace: loc.pathname.startsWith("/search") });
  };

  const logout = async () => {
    await api.post("/api/logout");
    api.invalidate();
    setUser(null);
  };

  return (
    <header className={`nav ${scrolled ? "scrolled" : ""}`}>
      <Logo />
      <nav className="nav-links">
        <NavLink to="/" end><Home size={18} /><span>Home</span></NavLink>
        <NavLink to="/libraries" end><Grid size={18} /><span>Libraries</span></NavLink>
        {libs?.map((l) => (
          <NavLink key={l.id} to={`/library/${l.id}`} className="lib-link">
            {l.kind === "movies" ? <Film size={18} /> : <Tv size={18} />}
            <span>{l.name}</span>
          </NavLink>
        ))}
      </nav>
      <div className="nav-right">
        <label className="search-box">
          <Search size={18} />
          <input ref={input} value={q} placeholder="Search" onChange={(e) => onSearch(e.target.value)}
            onKeyDown={(e) => e.key === "Escape" && (onSearch(""), input.current?.blur())} />
          <kbd>/</kbd>
        </label>
        {user?.isAdmin && <NavLink to="/settings" className="icon-btn" aria-label="Settings"><Settings /></NavLink>}
        <button className="avatar" onClick={logout} title={`Signed in as ${user?.username}. Click to sign out.`}>
          <span>{user?.username.slice(0, 1).toUpperCase()}</span>
          <Logout size={14} />
        </button>
      </div>
    </header>
  );
}
