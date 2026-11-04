import { FormEvent, StrictMode, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Navigate, Route, Routes, useLocation, useNavigate, useParams } from "react-router-dom";
import { api, art, Card, Detail, episodeLabel, Library, User } from "../api";
import { Play, Search as SearchIcon } from "../components/Icons";
import { UserContext, useUser } from "../hooks";
import { native, NativeEnded } from "../native";
import { Backdrop, BackdropCtx, Spinner, TvButton } from "./components";
import { focusEl, focusInitial, focusables, installKeys, onBack } from "./focus";
import { focusPage, setCurrentKey, ToastCtx, useLibraries, useToast } from "./hooks";
import { lastPlayedItem, onPlayBusy, playItem } from "./play";
import Home from "./pages/Home";
import Item from "./pages/Item";
import LibraryPage from "./pages/Library";
import SearchPage from "./pages/Search";
import SettingsPage from "./pages/Settings";
import { Screensaver } from "./Screensaver";
import { MenuProvider, useLibraryMenu, useLongPress } from "./menu";
import "./tv.css";

document.documentElement.classList.add("tv");
installKeys();

// ---------- tab bar ----------

/**
 * Apple TV style tab bar across the top: Search, Home, each library and
 * Settings. Up from the top of a page enters it, Down leaves it back to
 * where you were. It fades away while you browse further down a page.
 */
function TabBar({ hidden }: { hidden: boolean }) {
  const { data: libs } = useLibraries();
  const { user } = useUser();
  const nav = useNavigate();
  const loc = useLocation();

  const tabs: { key: string; to: string; label?: string; icon?: JSX.Element; lib?: Library }[] = [
    { key: "search", to: "/search", icon: <SearchIcon size={24} /> },
    { key: "home", to: "/", label: "Home" },
    ...(libs || []).filter((l) => !l.hidden).map((l) => ({ key: `lib${l.id}`, to: `/library/${l.id}`, label: l.name, lib: l })),
  ];
  const active = (to: string) => (to === "/" ? loc.pathname === "/" : loc.pathname.startsWith(to));
  const anyActive = tabs.some((t) => active(t.to)) || loc.pathname.startsWith("/settings");

  return (
    <nav className={`tv-nav ${hidden ? "away" : ""}`} data-group="nav" data-remember="false" data-enter="up">
      <div className="tv-nav-logo"><img src="/favicon.svg" alt="" /><span>plex<b>odus</b></span></div>
      <div className="tv-tabs-pill" data-hscroll="reveal">
        {tabs.map((t) => (
          <Tab key={t.key} fk={`nav-${t.key}`} isDefault={active(t.to) || (!anyActive && t.key === "home")} active={active(t.to)}
            icon={t.icon} label={t.label} lib={t.lib} onSelect={() => (active(t.to) ? focusPage() : nav(t.to))} />
        ))}
      </div>
      <button data-f data-fk="nav-settings" data-default={loc.pathname.startsWith("/settings") ? "" : undefined}
        className={`tv-avatar ${loc.pathname.startsWith("/settings") ? "active" : ""}`} aria-label="Settings"
        onClick={() => nav("/settings")}>
        {(user?.username || "?").slice(0, 1).toUpperCase()}
      </button>
    </nav>
  );
}

function Tab({ fk, isDefault, active, icon, label, lib, onSelect }: {
  fk: string; isDefault: boolean; active: boolean; icon?: JSX.Element; label?: string; lib?: Library; onSelect: () => void;
}) {
  const libMenu = useLibraryMenu();
  const press = useLongPress(onSelect, lib ? (el) => libMenu(lib, el) : undefined);
  return (
    <button data-f data-fk={fk} data-default={isDefault ? "" : undefined}
      className={`tv-tab-btn ${active ? "active" : ""} ${icon ? "icon" : ""}`}
      aria-label={label || fk} onClick={onSelect} {...press}>
      {icon}
      {label && <span>{label}</span>}
    </button>
  );
}

// ---------- up next ----------

function UpNext({ c, onPlay, onCancel }: { c: Card; onPlay: () => void; onCancel: () => void }) {
  const [n, setN] = useState(15);
  useEffect(() => {
    if (n <= 0) {
      onPlay();
      return;
    }
    const t = setTimeout(() => setN((x) => x - 1), 1000);
    return () => clearTimeout(t);
  }, [n, onPlay]);
  useEffect(() => onBack(() => (onCancel(), true)), [onCancel]);
  useEffect(() => {
    requestAnimationFrame(() => focusInitial());
  }, []);
  return (
    <div className="tv-upnext" data-scope>
      <div className="tv-upnext-card">
        <div className="tv-upnext-img" style={{ backgroundImage: `url(${art(c.backdrop || c.showBackdrop, "w780")})` }}>
          <div className="tv-upnext-count"><span>{n}</span></div>
        </div>
        <div className="tv-upnext-body">
          <div className="tv-eyebrow">Up next</div>
          <div className="tv-upnext-title">{c.showTitle || c.title}</div>
          <div className="tv-upnext-sub">{episodeLabel(c)} · {c.title}</div>
          <div className="tv-actions">
            <TvButton primary autoFocus onClick={onPlay} icon={<Play size={22} />}>Play now</TvButton>
            <TvButton onClick={onCancel}>Cancel</TvButton>
          </div>
        </div>
      </div>
    </div>
  );
}

// ---------- shell ----------

function PlayRoute() {
  const { id } = useParams();
  const nav = useNavigate();
  const toast = useToast();
  useEffect(() => {
    playItem(Number(id)).catch((e: Error) => toast(e.message));
    nav(-1);
  }, [id]);
  return null;
}

function Shell() {
  const loc = useLocation();
  const nav = useNavigate();
  const [backdrop, setBackdrop] = useState<string>();
  const [busy, setBusy] = useState<string | null>(null);
  const [toastMsg, setToastMsg] = useState<string | null>(null);
  const [upNext, setUpNext] = useState<Card | null>(null);
  const toastTimer = useRef<ReturnType<typeof setTimeout>>();
  // The tab bar shows at the top of a page and fades while you browse below.
  const [navAway, setNavAway] = useState(false);
  useEffect(() => {
    // Shown while you're in it, on a page's top section, or with the page
    // scrolled to the top; hidden once the page scrolls down under it.
    const update = () => {
      const el = document.activeElement as HTMLElement | null;
      if (!el || el === document.body || el.closest("[data-scope]")) return;
      if (el.closest(".tv-nav") || el.closest("[data-row-top]")) return setNavAway(false);
      const sc = el.closest<HTMLElement>("[data-vscroll]");
      setNavAway(!!sc && sc.scrollTop > 8);
    };
    document.addEventListener("focusin", update);
    document.addEventListener("scroll", update, true);
    return () => {
      document.removeEventListener("focusin", update);
      document.removeEventListener("scroll", update, true);
    };
  }, []);

  useLayoutEffect(() => {
    setCurrentKey(loc.key);
  }, [loc.key]);

  useEffect(() => onPlayBusy(setBusy), []);

  const toast = useCallback((msg: string) => {
    setToastMsg(msg);
    clearTimeout(toastTimer.current);
    toastTimer.current = setTimeout(() => setToastMsg(null), 4500);
  }, []);

  // Back: leave the page; on Home, go to the tab bar first, then exit.
  useEffect(
    () =>
      onBack(() => {
        if (loc.pathname !== "/") {
          if ((window.history.state?.idx ?? 0) > 0) nav(-1);
          else nav("/", { replace: true });
          return true;
        }
        if (!document.activeElement?.closest(".tv-nav")) {
          const home = focusables().find((el) => el.dataset.fk === "nav-home");
          return focusEl(home, true);
        }
        return false;
      }),
    [loc.pathname, nav],
  );

  // The app's player closed: refresh what's on screen and offer the next episode.
  useEffect(() => {
    const onEnded = async (e: Event) => {
      const d = (e as CustomEvent<NativeEnded>).detail;
      // Pages refetch in place (keeping what's on screen and the focus).
      window.dispatchEvent(new Event("tv:refresh"));
      const it = lastPlayedItem();
      if (!d?.ended || !it || it.id !== d.itemId || it.kind !== "episode" || !it.showId) return;
      await new Promise((r) => setTimeout(r, 400));
      const show = await api.get<Detail>(`/api/items/${it.showId}`).catch(() => null);
      if (show?.onDeck && show.onDeck.id !== it.id) setUpNext({ ...show.onDeck, showTitle: show.title, showBackdrop: show.backdrop });
    };
    window.addEventListener("plexodus:native-ended", onEnded);
    return () => window.removeEventListener("plexodus:native-ended", onEnded);
  }, []);

  const playNext = useCallback(() => {
    const c = upNext;
    setUpNext(null);
    if (c) playItem(c.id).catch((e: Error) => toast(e.message));
  }, [upNext, toast]);
  const cancelNext = useCallback(() => {
    setUpNext(null);
    requestAnimationFrame(focusPage);
  }, []);

  return (
    <ToastCtx.Provider value={toast}>
      <BackdropCtx.Provider value={setBackdrop}>
        <MenuProvider>
        <div className="tv-app">
          <Backdrop url={backdrop} />
          <main className="tv-main">
            <Routes>
              <Route path="/" element={<Home />} />
              <Route path="/library/:id" element={<LibraryPage />} />
              <Route path="/item/:id" element={<Item />} />
              <Route path="/search" element={<SearchPage />} />
              <Route path="/settings" element={<SettingsPage />} />
              <Route path="/play/:id" element={<PlayRoute />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </main>
          <TabBar hidden={navAway} />
          {upNext && <UpNext c={upNext} onPlay={playNext} onCancel={cancelNext} />}
          {busy && <div className="tv-busy"><Spinner label={busy} /></div>}
          {toastMsg && <div className="tv-toast">{toastMsg}</div>}
          <Screensaver onError={toast} />
        </div>
        </MenuProvider>
      </BackdropCtx.Provider>
    </ToastCtx.Provider>
  );
}

// ---------- sign in ----------

function Login({ setup }: { setup: boolean }) {
  const { setUser } = useUser();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    requestAnimationFrame(() => focusInitial());
  }, []);
  const submit = async (e?: FormEvent) => {
    e?.preventDefault();
    if (!username || !password) return;
    setBusy(true);
    setError(undefined);
    try {
      setUser(await api.post<User>(setup ? "/api/setup" : "/api/login", { username, password }));
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="tv-app tv-login">
      <div className="tv-grain" />
      <form className="tv-login-card" onSubmit={submit}>
        <div className="tv-login-logo"><img src="/favicon.svg" alt="" /><span>plex<b>odus</b></span></div>
        <p className="tv-muted">{setup ? "Create the admin account to get started." : "Sign in to your library."}</p>
        <input data-f data-autofocus placeholder="Username" value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" />
        <input data-f type="password" placeholder="Password" value={password} onChange={(e) => setPassword(e.target.value)}
          autoComplete={setup ? "new-password" : "current-password"} />
        {error && <div className="tv-error">{error}</div>}
        <button data-f className="tv-btn primary wide" type="submit" disabled={busy}>
          <span>{busy ? "Signing in…" : setup ? "Create account" : "Sign in"}</span>
        </button>
        {native && (
          <button data-f type="button" className="tv-btn ghost wide" onClick={() => native?.changeServer()}>
            <span>Connected to {window.location.host} · Change server</span>
          </button>
        )}
      </form>
    </div>
  );
}

// ---------- root ----------

function App() {
  const [state, setState] = useState<{ loading: boolean; setup: boolean; user: User | null; error?: string }>({ loading: true, setup: false, user: null });

  const load = useCallback(async () => {
    try {
      const status = await api.get<{ setupRequired: boolean }>("/api/status");
      const user = status.setupRequired ? null : await api.get<User>("/api/me").catch(() => null);
      setState({ loading: false, setup: status.setupRequired, user });
    } catch (e) {
      setState({ loading: false, setup: false, user: null, error: (e as Error).message });
    }
  }, []);

  useEffect(() => {
    load();
    const onUnauth = () => setState((s) => ({ ...s, user: null }));
    window.addEventListener("plexodus:unauthorized", onUnauth);
    return () => window.removeEventListener("plexodus:unauthorized", onUnauth);
  }, [load]);

  const setUser = (user: User | null) => setState((s) => ({ ...s, user, setup: false }));

  if (state.loading) return <div className="tv-app"><Spinner /></div>;
  if (state.error) {
    return (
      <div className="tv-app tv-center">
        <h1 className="tv-h1">Can't reach the server</h1>
        <p className="tv-muted">{state.error}</p>
        <div className="tv-actions">
          <TvButton primary autoFocus onClick={load}>Try again</TvButton>
          {native && <TvButton onClick={() => native?.changeServer()}>Change server</TvButton>}
        </div>
      </div>
    );
  }
  return (
    <UserContext.Provider value={{ user: state.user, setUser }}>
      {state.user ? (
        <BrowserRouter>
          <Shell />
        </BrowserRouter>
      ) : (
        <Login setup={state.setup} />
      )}
    </UserContext.Provider>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
