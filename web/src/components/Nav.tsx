import { useEffect, useRef, useState } from "react";
import { Link, NavLink, useLocation, useNavigate } from "react-router-dom";
import { Activity, api, Library, looksLikeQuestion } from "../api";
import { useApi, useUser } from "../hooks";
import { deviceLipSync } from "../native";
import { backToTv, canReturnToTv } from "../tv/detect";
import { Clock, Film, Grid, Home, Logout, Pulse, Search, Settings, Sparkle, Tv } from "./Icons";
import { ProfileAvatar } from "./Profiles";

export function Logo() {
  return (
    <Link to="/" className="logo" aria-label="Plexodus home">
      <img src="/favicon.svg" alt="" width={28} height={28} />
      <span>plex<b>odus</b></span>
    </Link>
  );
}

/** Admins' link to Now watching, with how many streams are live. */
function NowWatchingLink({ tab = false }: { tab?: boolean }) {
  const [n, setN] = useState(0);
  useEffect(() => {
    let alive = true;
    const load = () => {
      if (document.visibilityState !== "visible") return;
      api.get<Activity>("/api/activity?recent=0").then((a) => alive && setN(a.streams.length), () => {});
    };
    load();
    const t = setInterval(load, 20000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);
  return (
    <NavLink to="/activity" className={tab ? "np-link" : "icon-btn np-link"} aria-label={`Now watching${n ? `: ${n}` : ""}`} title="Now watching">
      <Pulse size={tab ? 22 : undefined} />
      {n > 0 && <span className="np-badge">{n}</span>}
      {tab && <span>Watching</span>}
    </NavLink>
  );
}

export function Nav() {
  const { user, setUser, openProfiles } = useUser();
  const [menu, setMenu] = useState(false);
  useEffect(() => {
    if (!menu) return;
    const close = () => setMenu(false);
    window.addEventListener("click", close);
    return () => window.removeEventListener("click", close);
  }, [menu]);
  const { data: libs, reload: reloadLibs } = useApi<Library[]>("/api/libraries");
  useEffect(() => {
    window.addEventListener("plexodus:library-prefs", reloadLibs);
    return () => window.removeEventListener("plexodus:library-prefs", reloadLibs);
  }, [reloadLibs]);
  const nav = useNavigate();
  const loc = useLocation();

  // Phone-style view on a TV: an obvious, already-focused way back, and the
  // remote's Back on Home returns to the TV view instead of leaving the app.
  const [tvBack] = useState(canReturnToTv);
  const tvBtn = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (tvBack) tvBtn.current?.focus();
  }, [tvBack]);
  const atHome = loc.pathname === "/";
  useEffect(() => {
    if (!tvBack) return;
    const w = window as unknown as { __plexodusBack?: () => string };
    w.__plexodusBack = () => (atHome ? (backToTv(), "handled") : "none");
    return () => {
      delete w.__plexodusBack;
    };
  }, [tvBack, atHome]);
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

  // Store policy: anyone who can make an account in the app can delete it there.
  const [confirmDelete, setConfirmDelete] = useState(false);
  const deleteAccount = async () => {
    if (!confirmDelete) return setConfirmDelete(true);
    try {
      await api.del("/api/me");
      api.invalidate();
      setUser(null);
    } catch (e) {
      alert(e instanceof Error ? e.message : String(e));
    }
  };

  return (
    <>
    <header className={`nav ${scrolled ? "scrolled" : ""}`}>
      <Logo />
      <nav className="nav-links">
        <NavLink to="/" end><Home size={18} /><span>Home</span></NavLink>
        <NavLink to="/libraries" end><Grid size={18} /><span>Libraries</span></NavLink>
        {libs?.filter((l) => !l.hidden).map((l) => (
          <NavLink key={l.id} to={`/library/${l.id}`} className="lib-link">
            {l.kind === "movies" ? <Film size={18} /> : <Tv size={18} />}
            <span>{l.name}</span>
          </NavLink>
        ))}
        {/* After the libraries, so they stay together. */}
        <NavLink to="/scene" end aria-label="Scene notes" title="Scene notes"><Sparkle size={18} /><span>Scene notes</span></NavLink>
        <NavLink to="/history" end aria-label="History" title="History"><Clock size={18} /><span>History</span></NavLink>
      </nav>
      <div className="nav-right">
        {tvBack && (
          <button ref={tvBtn} className="btn primary tv-back-btn" data-testid="back-to-tv" onClick={backToTv}>
            <Tv size={18} /><span>Back to TV view</span>
          </button>
        )}
        <label className="search-box">
          <Search size={18} />
          <input ref={input} value={q} placeholder="Search or ask…" onChange={(e) => onSearch(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") onSearch(""), input.current?.blur();
              // Enter asks in plain English ("90s heist films I haven't seen").
              // A word or two is a title: its matches are already on screen
              // (and the phone keyboard's Search key is Enter), so just put
              // the keyboard away.
              if (e.key === "Enter" && q.trim()) {
                if (looksLikeQuestion(q)) nav(`/search?q=${encodeURIComponent(q)}&ask=1`, { replace: loc.pathname.startsWith("/search") });
                else input.current?.blur();
              }
            }} />
          <kbd>/</kbd>
        </label>
        {user?.isAdmin && !user.isProfile && <span className="np-top"><NowWatchingLink /></span>}
        {(user?.isAdmin || deviceLipSync()) && <NavLink to="/settings" className="icon-btn" aria-label="Settings"><Settings /></NavLink>}
        <div className="nav-profile">
          <button className="nav-avatar-btn" aria-label="Profile menu" title={user?.name || user?.username}
            onClick={(e) => (e.stopPropagation(), setMenu(!menu))}>
            {user && <ProfileAvatar p={{ id: user.id, name: user.name || user.username, avatar: user.avatar, color: user.color }} size={36} />}
          </button>
          {menu && (
            <div className="pf-menu" onClick={(e) => e.stopPropagation()}>
              <div className="muted small" style={{ padding: "4px 10px" }}>
                {user?.name || user?.username}{user?.kids ? " · Kids" : ""}
              </div>
              <button onClick={() => (setMenu(false), openProfiles?.())}>Switch profile</button>
              {!user?.isProfile && <button onClick={() => (setMenu(false), openProfiles?.(true))}>Manage profiles</button>}
              <hr />
              <button onClick={logout}><Logout size={16} /> Sign out{user?.isProfile ? ` of ${user.username}` : ""}</button>
              {user && !user.isAdmin && !user.isProfile && (
                <button className="danger" onClick={deleteAccount}>
                  {confirmDelete ? "Click again to delete your account" : "Delete my account"}
                </button>
              )}
            </div>
          )}
        </div>
      </div>
    </header>
    {/* Phones: the main sections live in a tab bar at the bottom so the top
        bar only holds search, settings and the profile. */}
    <nav className="tabbar" aria-label="Sections">
      <NavLink to="/" end><Home size={22} /><span>Home</span></NavLink>
      <NavLink to="/libraries"><Grid size={22} /><span>Libraries</span></NavLink>
      <NavLink to="/history" end><Clock size={22} /><span>History</span></NavLink>
      <NavLink to="/scene" end><Sparkle size={22} /><span>Scene notes</span></NavLink>
      {user?.isAdmin && !user.isProfile && <NowWatchingLink tab />}
    </nav>
    </>
  );
}
