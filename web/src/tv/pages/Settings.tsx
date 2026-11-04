import { useState } from "react";
import { api, Library, LibraryPrefs, setLibraryPrefs } from "../../api";
import { Check, Logout, Refresh, X } from "../../components/Icons";
import { useUser } from "../../hooks";
import { native } from "../../native";
import { TvButton, useBackdrop } from "../components";
import { useLibraries, usePageFocus, useToast } from "../hooks";

/** On/off switch for one library choice. */
function Toggle({ on, fk, label, onClick }: { on: boolean; fk: string; label: string; onClick: () => void }) {
  return (
    <button data-f data-fk={fk} className={`tv-btn chip tv-toggle ${on ? "on" : ""}`} aria-pressed={on} onClick={onClick}>
      {on ? <Check size={18} /> : <X size={18} />}
      <span>{label}</span>
    </button>
  );
}

/** Which libraries appear in the menu across the top, and which feed Home. */
function LibraryChoices() {
  const { data: libs } = useLibraries();
  const toast = useToast();
  const [saving, setSaving] = useState(false);
  if (!libs?.length) return null;

  const change = async (l: Library, prefs: LibraryPrefs) => {
    if (saving) return;
    setSaving(true);
    try {
      await setLibraryPrefs(l.id, prefs);
    } catch (e) {
      toast((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className="tv-lib-prefs">
      <h2 className="tv-section-title">Libraries</h2>
      <p className="tv-muted tv-section-note">Choose which libraries appear in the menu at the top, and which ones fill your Home screen.</p>
      {libs.map((l) => (
        <div key={l.id} className="tv-pref-row" data-row data-group={`libpref-${l.id}`}>
          <div className="tv-pref-name">
            <span>{l.name}</span>
            <span className="tv-muted">{l.count.toLocaleString()} {l.kind === "movies" ? "films" : "shows"}</span>
          </div>
          <Toggle fk={`lib-${l.id}-menu`} on={!l.hidden} label="In menu" onClick={() => change(l, { hidden: !l.hidden })} />
          <Toggle fk={`lib-${l.id}-home`} on={!l.hiddenHome} label="On Home" onClick={() => change(l, { hiddenHome: !l.hiddenHome })} />
        </div>
      ))}
    </section>
  );
}

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
    <div className="tv-page tv-settings" data-vscroll="reveal" data-margin="5rem">
      <h1 className="tv-h1">Settings</h1>
      <dl className="tv-facts">
        <dt>Signed in as</dt><dd>{user?.username}{user?.isAdmin ? " (admin)" : ""}</dd>
        <dt>Server</dt><dd>{window.location.host}</dd>
        {version && <><dt>App version</dt><dd>{version}</dd></>}
      </dl>
      <div className="tv-settings-actions" data-row data-row-top>
        <TvButton autoFocus fk="reload" icon={<Refresh size={22} />} onClick={() => window.location.reload()}>Reload</TvButton>
        {native && <TvButton fk="server" onClick={() => native?.changeServer()}>Change server</TvButton>}
        <TvButton fk="classic" onClick={classic}>Use the phone-style interface</TvButton>
        <TvButton fk="logout" icon={<Logout size={22} />} onClick={signOut}>Sign out</TvButton>
      </div>
      <LibraryChoices />
      <p className="tv-muted tv-note">Adding libraries, users and the Plex import live in the Plexodus web page on a computer.</p>
      <div className="tv-page-tail" />
    </div>
  );
}
