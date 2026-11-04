import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Navigate, Route, Routes, useLocation } from "react-router-dom";
import { api, User } from "./api";
import { Nav } from "./components/Nav";
import { WhoIsWatching } from "./components/Profiles";
import { forgetPicked, goHome, loadProfiles, shouldPick } from "./profiles";
import { UserContext } from "./hooks";
import Home from "./pages/Home";
import Item from "./pages/Item";
import Libraries from "./pages/Libraries";
import Library from "./pages/Library";
import Login from "./pages/Login";
import Player from "./pages/Player";
import Search from "./pages/Search";
import Settings from "./pages/Settings";
import "./styles.css";

function Shell() {
  const loc = useLocation();
  const playing = loc.pathname.startsWith("/play/");
  useEffect(() => {
    if (!loc.pathname.startsWith("/search")) window.scrollTo(0, 0);
  }, [loc.pathname]);
  return (
    <>
      {!playing && <Nav />}
      <main className={playing ? "" : "main"}>
        <Routes>
          <Route path="/" element={<Home />} />
          <Route path="/libraries" element={<Libraries />} />
          <Route path="/library/:id" element={<Library />} />
          <Route path="/item/:id" element={<Item />} />
          <Route path="/play/:id" element={<Player />} />
          <Route path="/search" element={<Search />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </>
  );
}

function App() {
  const [state, setState] = useState<{ loading: boolean; setup: boolean; user: User | null }>({ loading: true, setup: false, user: null });
  // "Who's watching?": shown on start when the account has profiles, or on demand.
  const [picker, setPicker] = useState<null | "pick" | "manage" | "start">(null);

  useEffect(() => {
    (async () => {
      const status = await api.get<{ setupRequired: boolean }>("/api/status");
      const user = status.setupRequired ? null : await api.get<User>("/api/me").catch(() => null);
      if (user) {
        const info = await loadProfiles().catch(() => null);
        if (info && shouldPick(info)) setPicker("start");
      }
      setState({ loading: false, setup: status.setupRequired, user });
    })();
    const onUnauth = () => setState((s) => ({ ...s, user: null }));
    window.addEventListener("plexodus:unauthorized", onUnauth);
    return () => window.removeEventListener("plexodus:unauthorized", onUnauth);
  }, []);

  const setUser = (user: User | null) => {
    if (!user) forgetPicked();
    else if (!state.user) {
      // Just signed in: ask who's watching if the account has profiles.
      loadProfiles().then((info) => shouldPick(info) && setPicker("start"), () => {});
    }
    setState((s) => ({ ...s, user, setup: false }));
  };
  const openProfiles = (manage?: boolean) => setPicker(manage ? "manage" : "pick");

  if (state.loading) return <div className="page-loading full"><div className="spinner" /></div>;
  return (
    <UserContext.Provider value={{ user: state.user, setUser, openProfiles }}>
      {state.user && picker ? (
        <WhoIsWatching manage={picker === "manage"} onClose={picker === "start" ? undefined : () => setPicker(null)}
          onPicked={(u) => (goHome(), setPicker(null), setUser(u))} />
      ) : state.user ? (
        <BrowserRouter key={state.user.id}>
          <Shell />
        </BrowserRouter>
      ) : (
        <Login setup={state.setup} invite={window.location.pathname.match(/^\/invite\/([a-f0-9]+)$/)?.[1]} />
      )}
    </UserContext.Provider>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
