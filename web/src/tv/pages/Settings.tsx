import { api } from "../../api";
import { Logout, Refresh } from "../../components/Icons";
import { useUser } from "../../hooks";
import { native } from "../../native";
import { TvButton, useBackdrop } from "../components";
import { usePageFocus } from "../hooks";

export default function SettingsPage() {
  const { user, setUser } = useUser();
  useBackdrop(undefined);
  usePageFocus(true);
  let version = "";
  try {
    version = native?.version() || "";
  } catch {
    /* older app */
  }

  const signOut = async () => {
    await api.post("/api/logout").catch(() => {});
    api.invalidate();
    setUser(null);
  };

  const classic = () => {
    try {
      sessionStorage.setItem("plexodus.tv.session", "0");
    } catch {
      /* ignore */
    }
    window.location.href = "/";
  };

  return (
    <div className="tv-page tv-settings">
      <h1 className="tv-h1">Settings</h1>
      <dl className="tv-facts">
        <dt>Signed in as</dt><dd>{user?.username}{user?.isAdmin ? " (admin)" : ""}</dd>
        <dt>Server</dt><dd>{window.location.host}</dd>
        {version && <><dt>App version</dt><dd>{version}</dd></>}
      </dl>
      <div className="tv-settings-actions">
        <TvButton autoFocus fk="reload" icon={<Refresh size={22} />} onClick={() => window.location.reload()}>Reload</TvButton>
        {native && <TvButton fk="server" onClick={() => native?.changeServer()}>Change server</TvButton>}
        <TvButton fk="classic" onClick={classic}>Use the phone-style interface</TvButton>
        <TvButton fk="logout" icon={<Logout size={22} />} onClick={signOut}>Sign out</TvButton>
      </div>
      <p className="tv-muted tv-note">Library management, users and the Plex import live in the Plexodus web page on a computer.</p>
    </div>
  );
}
