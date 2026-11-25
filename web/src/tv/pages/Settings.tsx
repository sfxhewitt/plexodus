import { KeyboardEvent, ReactNode, useEffect, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { Activity, api, fmtTime, Library, LibraryPrefs, setLibraryPrefs } from "../../api";
import { Check, Grid, Logout, Play, Pulse, Refresh, Sparkle, Tv, User as UserIcon, X } from "../../components/Icons";
import { useUser } from "../../hooks";
import { native, nativeHas, phoneLipSync } from "../../native";
import { ambientPrefs, onAmbientPrefs, setAmbientPrefs, startScreensaver, trailersSupported } from "../ambient";
import { TvButton, useBackdrop } from "../components";
import { focusEl, onBack } from "../focus";
import { useLibraries, usePageFocus, useToast } from "../hooks";
import { rtPostersOn, setRtPosters } from "../../rt";

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

/** Rotten Tomatoes overlays on posters, kept on this TV. */
function RatingChoices() {
  const [on, setOn] = useState(rtPostersOn);
  return (
    <section className="tv-lib-prefs">
      <h2 className="tv-section-title">Ratings</h2>
      <p className="tv-muted tv-section-note">
        Rotten Tomatoes scores come from your Plex server: the critics' Tomatometer (tomato or splat) and the audience
        score (popcorn). They always show on title pages; this choice is for the small badge on posters, on this TV only.
      </p>
      <div className="tv-pref-row" data-row data-group="rt-posters">
        <div className="tv-pref-name"><span>Scores on posters</span></div>
        <Toggle fk="rt-posters" on={on} label={on ? "Shown" : "Hidden"} onClick={() => { setRtPosters(!on); setOn(!on); }} />
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
  const phone = phoneLipSync();
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
        {phone
          ? " Fix with my phone shows a flash-and-beep test and a QR code: scan it, film the TV for ten seconds and this TV sets itself. No guessing which way."
          : " Calibrate plays a beep with a flash: nudge with left and right until they land together, then press OK."}
        {" "}For one bad file, use Audio sync in the player instead.
      </p>
      <div className="tv-pref-row" data-row data-group="lipsync">
        <div className="tv-pref-name">
          <span>This TV</span>
          <span className="tv-muted">{supported ? fmtSync(ms) : "Update the Plexodus app on this TV to use this"}</span>
        </div>
        {supported && (
          <>
            {phone && <TvButton fk="lipsync-phone" className="chip ambient" onClick={() => native?.calibrateWithPhone?.()}>Fix with my phone</TvButton>}
            <TvButton fk="lipsync-calibrate" className="chip ambient" onClick={() => native?.calibrateAudio?.()}>{phone ? "Calibrate by eye" : "Calibrate"}</TvButton>
            <TvButton fk="lipsync-earlier" className="chip ambient" onClick={() => change(ms - 20)}>Sound earlier</TvButton>
            <TvButton fk="lipsync-later" className="chip ambient" onClick={() => change(ms + 20)}>Sound later</TvButton>
            <TvButton fk="lipsync-reset" className="chip ambient" onClick={() => change(0)}>Reset</TvButton>
          </>
        )}
      </div>
    </section>
  );
}

/**
 * Which engine the TV player uses. ExoPlayer is the one Plex's own TV app
 * plays through: it times every frame to the screen and follows the sound
 * card's clock. libVLC is the original engine, kept as a fallback.
 */
function EngineChoice() {
  const read = () => {
    try {
      return native?.playerEngine?.() || "vlc";
    } catch {
      return "vlc";
    }
  };
  const [engine, setEngine] = useState(read);
  if (!native || !nativeHas("engine")) return null;
  const pick = (e: string) => {
    native?.setPlayerEngine?.(e);
    setEngine(e);
  };
  return (
    <section className="tv-lib-prefs">
      <h2 className="tv-section-title">Player engine</h2>
      <p className="tv-muted tv-section-note">
        ExoPlayer is the engine Plex uses: picture and sound stay locked together on their own. libVLC is the original
        engine; files ExoPlayer can't open play on it anyway. Takes effect from the next thing you play.
      </p>
      <div className="tv-pref-row" data-row data-group="engine">
        <div className="tv-pref-name">
          <span>This TV</span>
          <span className="tv-muted">{engine === "exo" ? "ExoPlayer (like Plex)" : "libVLC"}</span>
        </div>
        <TvButton fk="engine-exo" className={"chip ambient" + (engine === "exo" ? " on" : "")} onClick={() => pick("exo")}>ExoPlayer (like Plex)</TvButton>
        <TvButton fk="engine-vlc" className={"chip ambient" + (engine === "vlc" ? " on" : "")} onClick={() => pick("vlc")}>libVLC</TvButton>
      </div>
    </section>
  );
}

/** Frame-rate matching: off by default, since the switch blanks the TV and Plex doesn't do it. */
function FrameRateChoice() {
  const read = () => {
    try {
      return !!native?.matchFrameRate?.();
    } catch {
      return false;
    }
  };
  const [on, setOn] = useState(read);
  if (!native || !nativeHas("framerate-option")) return null;
  const pick = (v: boolean) => {
    native?.setMatchFrameRate?.(v);
    setOn(v);
  };
  return (
    <section className="tv-lib-prefs">
      <h2 className="tv-section-title">Match frame rate</h2>
      <p className="tv-muted tv-section-note">
        Switches the TV to the film's frame rate when it starts. Films pan a little smoother, but the screen goes black for a
        moment while the TV switches. Plex doesn't do this.
      </p>
      <div className="tv-pref-row" data-row data-group="framerate">
        <div className="tv-pref-name">
          <span>This TV</span>
          <span className="tv-muted">{on ? "On" : "Off"}</span>
        </div>
        <TvButton fk="framerate-off" className={"chip ambient" + (!on ? " on" : "")} onClick={() => pick(false)}>Off</TvButton>
        <TvButton fk="framerate-on" className={"chip ambient" + (on ? " on" : "")} onClick={() => pick(true)}>On</TvButton>
      </div>
    </section>
  );
}

/** "Use the phone-style interface": the touch layout, until the app restarts. */
function PhoneViewChoice() {
  const classic = () => {
    try {
      sessionStorage.setItem("plexodus.tv.session", "0");
    } catch {
      /* ignore */
    }
    window.location.href = "/";
  };
  return (
    <section className="tv-lib-prefs">
      <h2 className="tv-section-title">Interface</h2>
      <p className="tv-muted tv-section-note">
        The phone-style interface is the touch layout phones and tablets get. It lasts until the app restarts, and a
        Back to TV view button brings you straight back.
      </p>
      <div className="tv-pref-row" data-row data-group="classic">
        <div className="tv-pref-name"><span>This TV</span><span className="tv-muted">TV interface</span></div>
        <TvButton fk="classic" className="chip ambient" onClick={classic}>Use the phone-style interface</TvButton>
      </div>
    </section>
  );
}

type Cat = { key: string; label: string; hint: string; icon: ReactNode; body: ReactNode };

/**
 * Settings in two panes: categories down the left, the chosen one's settings
 * on the right. Moving through the categories shows each one straight away;
 * Right (or OK) steps into it, Left or Back steps out again.
 */
export default function SettingsPage() {
  const { user, setUser, openProfiles } = useUser();
  const params = useParams();
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

  // Store policy: anyone who can make an account in the app can delete it there.
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleteError, setDeleteError] = useState("");
  const deleteAccount = async () => {
    if (!confirmDelete) return setConfirmDelete(true);
    try {
      await api.del("/api/me");
      api.invalidate();
      setUser(null);
    } catch (e) {
      setDeleteError(e instanceof Error ? e.message : String(e));
    }
  };

  const admin = !!user?.isAdmin && !user.isProfile;
  const cats: Cat[] = [
    {
      key: "account", label: "Account", hint: "Profiles and signing out", icon: <UserIcon size={24} />,
      body: (
        <>
          <dl className="tv-facts">
            <dt>Profile</dt><dd>{user?.name || user?.username}{user?.kids ? " (kids)" : ""}</dd>
            <dt>Signed in as</dt><dd>{user?.username}{user?.isAdmin ? " (admin)" : ""}</dd>
          </dl>
          <div className="tv-settings-actions" data-row data-row-top>
            <TvButton fk="profiles" onClick={() => openProfiles?.()}>Switch profile</TvButton>
            {!user?.isProfile && <TvButton fk="manage-profiles" onClick={() => openProfiles?.(true)}>Manage profiles</TvButton>}
            <TvButton fk="logout" icon={<Logout size={22} />} onClick={signOut}>Sign out</TvButton>
            {user && !user.isAdmin && !user.isProfile && (
              <TvButton fk="delete-account" onClick={deleteAccount}>
                {confirmDelete ? "Press again to delete your account" : "Delete my account"}
              </TvButton>
            )}
          </div>
          {deleteError && <p className="tv-muted tv-note">{deleteError}</p>}
        </>
      ),
    },
    { key: "libraries", label: "Libraries", hint: "Menu and Home choices", icon: <Grid size={24} />, body: <LibraryChoices /> },
    {
      key: "screen", label: "Look and feel", hint: "Screensaver, trailers, ratings", icon: <Sparkle size={24} />,
      body: (
        <>
          <AmbientChoices />
          <RatingChoices />
          {!user?.kids && <PhoneViewChoice />}
        </>
      ),
    },
    ...(native ? [{
      key: "playback", label: "Playback", hint: "Lip sync, player, frame rate", icon: <Play size={24} />,
      body: (
        <>
          <LipSyncChoices />
          <EngineChoice />
          <FrameRateChoice />
        </>
      ),
    }] : []),
    ...(admin ? [{ key: "activity", label: "Now watching", hint: "Who is playing what", icon: <Pulse size={24} />, body: null }] : []),
    {
      key: "app", label: "This TV", hint: "App, updates and server", icon: <Tv size={24} />,
      body: (
        <>
          <dl className="tv-facts">
            <dt>Server</dt><dd>{window.location.host}</dd>
            {version && <><dt>App version</dt><dd>{version}</dd></>}
          </dl>
          <div className="tv-settings-actions" data-row data-row-top>
            <TvButton fk="reload" icon={<Refresh size={22} />} onClick={() => window.location.reload()}>Reload</TvButton>
            {native && nativeHas("update") && (
              <TvButton fk="app-update" onClick={() => native?.checkForUpdates?.()}>Check for updates</TvButton>
            )}
            {native && <TvButton fk="server" onClick={() => native?.changeServer()}>Change server</TvButton>}
          </div>
          <p className="tv-muted tv-note">Adding libraries, users and the Plex import live in the Plexodus web page on a computer.</p>
        </>
      ),
    },
  ];

  const [picked, setPicked] = useState(params.section || "account");
  useEffect(() => {
    if (params.section) setPicked(params.section);
  }, [params.section]);
  const section = cats.some((c) => c.key === picked) ? picked : "account";
  const pane = useRef<HTMLDivElement>(null);
  const rail = useRef<HTMLElement>(null);
  // Each category starts at its top.
  useEffect(() => {
    if (pane.current) pane.current.scrollTop = 0;
  }, [section]);

  const railItem = () => rail.current?.querySelector<HTMLElement>(`[data-fk="set-${section}"]`);
  const enterPane = () => {
    const first = [...(pane.current?.querySelectorAll<HTMLElement>(".tv-set-sec:not([hidden]) [data-f]") || [])]
      .find((el) => !(el as HTMLButtonElement).disabled && el.getBoundingClientRect().width > 0);
    return focusEl(first, true);
  };
  // Back from a category's settings returns to the list of categories.
  useEffect(() => onBack(() => {
    const a = document.activeElement;
    if (!a || !pane.current?.contains(a) || document.querySelector("[data-scope]")) return false;
    return focusEl(railItem(), true);
  }));
  // Down from the tab bar comes back to the list, where Up left it.
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key !== "ArrowDown" || document.querySelector("[data-scope]")) return;
      if (!(document.activeElement as HTMLElement | null)?.closest(".tv-nav")) return;
      const item = railItem();
      if (!item) return;
      e.preventDefault();
      e.stopPropagation();
      focusEl(item, true);
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  });
  // The list is entered with Left and left with Right, so Up and Down stay in
  // it; Right always starts a category at its first setting, and Up from the
  // first category goes to the tab bar like the top of any page.
  const onRailKey = (e: KeyboardEvent, i: number) => {
    const toNav = e.key === "ArrowUp" && i === 0;
    if (e.key !== "ArrowRight" && !toNav) return;
    e.preventDefault();
    e.stopPropagation();
    if (toNav) focusEl(document.querySelector<HTMLElement>(".tv-nav [data-default]") || document.querySelector<HTMLElement>(".tv-nav [data-f]"), true);
    else enterPane();
  };

  return (
    <div className="tv-page tv-settings">
      {/* The pane comes first so the page's focus order starts in it; the list sits on the left. */}
      <div className="tv-set-pane">
        <header className="tv-set-head">
          <h2>{cats.find((c) => c.key === section)?.label}</h2>
        </header>
        {/* Every category stays mounted (hidden) so its switches keep their state as you move through the list. */}
        <div className="tv-set-scroll" ref={pane} data-vscroll="reveal" data-margin="14rem">
          {cats.map((c) => (
            <div key={c.key} className="tv-set-sec" data-section={c.key} hidden={c.key !== section}>
              {c.key === "activity" ? section === "activity" && <NowWatching /> : c.body}
            </div>
          ))}
          <div className="tv-page-tail" />
        </div>
      </div>
      <aside className="tv-set-rail" ref={rail} data-group="settings" data-enter="left" aria-label="Settings categories">
        <h1 className="tv-h1">Settings</h1>
        {cats.map((c, i) => (
          <button key={c.key} data-f data-fk={`set-${c.key}`} data-autofocus={c.key === section ? "" : undefined}
            data-default={c.key === section ? "" : undefined}
            className={`tv-set-cat ${c.key === section ? "active" : ""}`} aria-current={c.key === section}
            onFocus={() => setPicked(c.key)} onClick={enterPane} onKeyDown={(e) => onRailKey(e, i)}>
            {c.icon}
            <span className="l">{c.label}</span>
            <span className="h">{c.hint}</span>
          </button>
        ))}
      </aside>
    </div>
  );
}
