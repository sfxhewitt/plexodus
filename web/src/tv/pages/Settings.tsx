import { useEffect, useState } from "react";
import { Activity, api, fmtTime, Library, LibraryPrefs, setLibraryPrefs } from "../../api";
import { Check, Logout, Refresh, X } from "../../components/Icons";
import { useUser } from "../../hooks";
import { native, nativeHas } from "../../native";
import { ambientPrefs, onAmbientPrefs, setAmbientPrefs, startScreensaver, trailersSupported } from "../ambient";
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

/** Screensaver delay and trailer choices, kept on this TV. */
function AmbientChoices() {
  const [p, setP] = useState(ambientPrefs);
  useEffect(() => onAmbientPrefs(() => setP(ambientPrefs())), []);
  const canTrailers = trailersSupported();
  return (
    <section className="tv-lib-prefs">
      <h2 className="tv-section-title">Screensaver and trailers</h2>
      <p className="tv-muted tv-section-note">
        After a while untouched, the TV drifts through art from your libraries; press OK to play what's on screen.
        Rest on a film or show and its trailer fades in behind the art. These choices apply to this TV only.
      </p>
      <div className="tv-pref-row" data-row data-group="amb-saver">
        <div className="tv-pref-name"><span>Screensaver</span></div>
        {[0, 1, 3, 5, 10].map((m) => (
          <TvButton key={m} fk={`saver-${m}`} className={`chip ambient ${p.screensaver === m ? "on" : ""}`}
            onClick={() => setAmbientPrefs({ screensaver: m })}>
            {m ? `${m} min` : "Off"}
          </TvButton>
        ))}
        <TvButton fk="saver-preview" className="chip ambient" onClick={startScreensaver}>Preview</TvButton>
      </div>
      <div className="tv-pref-row" data-row data-group="amb-trailers">
        <div className="tv-pref-name">
          <span>Trailers</span>
          {!canTrailers && <span className="tv-muted">Update the Plexodus app on this TV to use trailers</span>}
        </div>
        {canTrailers && (
          <>
            <Toggle fk="trailers" on={p.trailers} label="Play trailers" onClick={() => setAmbientPrefs({ trailers: !p.trailers })} />
            <Toggle fk="trailer-sound" on={p.trailerSound} label="With sound" onClick={() => setAmbientPrefs({ trailerSound: !p.trailerSound })} />
          </>
        )}
      </div>
    </section>
  );
}

/** Who is watching what right now (admins only), refreshed every few seconds. */
function NowWatching() {
  const [data, setData] = useState<Activity>();
  useEffect(() => {
    let alive = true;
    const load = () => api.get<Activity>("/api/activity?recent=0").then((a) => alive && setData(a), () => {});
    load();
    const t = setInterval(load, 5000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);
  if (!data) return null;
  return (
    <section className="tv-lib-prefs">
      <h2 className="tv-section-title">Now watching</h2>
      {!data.streams.length && <p className="tv-muted tv-section-note">Nobody is watching right now.</p>}
      {data.streams.map((s) => {
        const title = s.kind === "episode" && s.showTitle
          ? `${s.showTitle} · S${s.seasonNumber ?? "?"} E${s.episodeNumber ?? "?"}`
          : s.title;
        return (
          <div key={s.key} className="tv-pref-row">
            <div className="tv-pref-name">
              <span>{s.profile ? `${s.username} · ${s.profile}` : s.username} — {title}</span>
              <span className="tv-muted">
                {s.state === "playing" ? "Playing" : "Paused"} · {fmtTime(s.position)} of {fmtTime(s.duration)} · {s.device} ·{" "}
                {s.method === "direct" ? "Direct play" : "Remux"}
              </span>
            </div>
          </div>
        );
      })}
    </section>
  );
}

const fmtSync = (ms: number) => (!ms ? "In sync" : `Sound ${Math.abs(ms)} ms ${ms > 0 ? "later" : "earlier"}`);

/**
 * This TV's lip sync offset: delay the TV, soundbar or receiver adds, which
 * no file can know about. Kept in the app and added to every file's own fix.
 */
function LipSyncChoices() {
  const supported = !!native && nativeHas("avsync");
  const read = () => {
    try {
      return native?.audioDelay?.() || 0;
    } catch {
      return 0;
    }
  };
  const [ms, setMs] = useState(read);
  useEffect(() => {
    const on = () => setMs(read());
    window.addEventListener("plexodus:audio-delay", on);
    return () => window.removeEventListener("plexodus:audio-delay", on);
  }, []);
  const change = (v: number) => {
    const next = Math.max(-5000, Math.min(5000, v));
    native?.setAudioDelay?.(next);
    setMs(next);
  };
  return (
    <section className="tv-lib-prefs">
      <h2 className="tv-section-title">Lip sync</h2>
      <p className="tv-muted tv-section-note">
        If voices are early or late on everything you play on this TV, the TV or sound system is adding delay.
        Calibrate plays a beep with a flash: nudge with left and right until they land together, then press OK.
        For one bad file, use Audio sync in the player instead.
      </p>
      <div className="tv-pref-row" data-row data-group="lipsync">
        <div className="tv-pref-name">
          <span>This TV</span>
          <span className="tv-muted">{supported ? fmtSync(ms) : "Update the Plexodus app on this TV to use this"}</span>
        </div>
        {supported && (
          <>
            <TvButton fk="lipsync-calibrate" className="chip ambient" onClick={() => native?.calibrateAudio?.()}>Calibrate</TvButton>
            <TvButton fk="lipsync-earlier" className="chip ambient" onClick={() => change(ms - 20)}>Sound earlier</TvButton>
            <TvButton fk="lipsync-later" className="chip ambient" onClick={() => change(ms + 20)}>Sound later</TvButton>
            <TvButton fk="lipsync-reset" className="chip ambient" onClick={() => change(0)}>Reset</TvButton>
          </>
        )}
      </div>
    </section>
  );
}

export default function SettingsPage() {
  const { user, setUser, openProfiles } = useUser();
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
        <dt>Profile</dt><dd>{user?.name || user?.username}{user?.kids ? " (kids)" : ""}</dd>
        <dt>Signed in as</dt><dd>{user?.username}{user?.isAdmin ? " (admin)" : ""}</dd>
        <dt>Server</dt><dd>{window.location.host}</dd>
        {version && <><dt>App version</dt><dd>{version}</dd></>}
      </dl>
      <div className="tv-settings-actions" data-row data-row-top>
        <TvButton autoFocus fk="profiles" onClick={() => openProfiles?.()}>Switch profile</TvButton>
        {!user?.isProfile && <TvButton fk="manage-profiles" onClick={() => openProfiles?.(true)}>Manage profiles</TvButton>}
        <TvButton fk="reload" icon={<Refresh size={22} />} onClick={() => window.location.reload()}>Reload</TvButton>
        {native && nativeHas("update") && (
          <TvButton fk="app-update" onClick={() => native?.checkForUpdates?.()}>Check for updates</TvButton>
        )}
        {native && <TvButton fk="server" onClick={() => native?.changeServer()}>Change server</TvButton>}
        {!user?.kids && <TvButton fk="classic" onClick={classic}>Use the phone-style interface</TvButton>}
        <TvButton fk="logout" icon={<Logout size={22} />} onClick={signOut}>Sign out</TvButton>
      </div>
      {user?.isAdmin && !user.isProfile && <NowWatching />}
      <LibraryChoices />
      <AmbientChoices />
      {native && <LipSyncChoices />}
      <p className="tv-muted tv-note">Adding libraries, users and the Plex import live in the Plexodus web page on a computer.</p>
      <div className="tv-page-tail" />
    </div>
  );
}
