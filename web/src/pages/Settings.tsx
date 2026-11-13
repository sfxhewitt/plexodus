import { FormEvent, useEffect, useState } from "react";
import { AdminUser, api, fmtSize, Invite, Library, PlexStatus, PlexSyncStatus, ScanProgress, User } from "../api";
import { Copy, Folder, Plus, Refresh, Trash, X } from "../components/Icons";
import { useApi, useUser } from "../hooks";

function ago(ts?: number | string) {
  if (!ts) return "never";
  const t = typeof ts === "number" ? ts * 1000 : Date.parse(ts);
  const s = Math.max(0, (Date.now() - t) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

function FolderPicker({ onPick, onClose }: { onPick: (p: string) => void; onClose: () => void }) {
  const [path, setPath] = useState("/");
  const { data, error } = useApi<{ path: string; parent: string; dirs: string[] }>(`/api/fs?path=${encodeURIComponent(path)}`);
  return (
    <div className="modal-back" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <h2>Choose a folder</h2>
          <button className="icon-btn" onClick={onClose} aria-label="Close"><X /></button>
        </div>
        <div className="fs-path">{data?.path || path}</div>
        {error && <div className="err">{error}</div>}
        <div className="fs-list">
          {data?.parent && <button onClick={() => setPath(data.parent)}>..</button>}
          {data?.dirs.map((d) => (
            <button key={d} onClick={() => setPath((data.path === "/" ? "" : data.path) + "/" + d)}><Folder size={16} /> {d}</button>
          ))}
        </div>
        <div className="modal-foot">
          <button className="btn primary" onClick={() => data && onPick(data.path)}>Use this folder</button>
        </div>
      </div>
    </div>
  );
}

function LibraryForm({ initial, onDone, onCancel }: { initial?: Library; onDone: () => void; onCancel: () => void }) {
  const [name, setName] = useState(initial?.name || "");
  const [kind, setKind] = useState<"movies" | "shows">(initial?.kind || "movies");
  const [paths, setPaths] = useState<string[]>(initial?.paths || [""]);
  const [trust, setTrust] = useState(initial?.trustDirMtime || false);
  const [picking, setPicking] = useState<number | null>(null);
  const [error, setError] = useState<string>();

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(undefined);
    const body = { name, kind, paths: paths.filter(Boolean), trustDirMtime: trust };
    try {
      if (initial) await api.put(`/api/libraries/${initial.id}`, body);
      else await api.post("/api/libraries", body);
      api.invalidate();
      onDone();
    } catch (err) {
      setError((err as Error).message);
    }
  };

  return (
    <form className="card form" onSubmit={submit}>
      <div className="form-row">
        <label>Name<input value={name} onChange={(e) => setName(e.target.value)} placeholder="Movies" /></label>
        <label>Type
          <select value={kind} onChange={(e) => setKind(e.target.value as "movies" | "shows")}>
            <option value="movies">Movies</option>
            <option value="shows">TV shows</option>
          </select>
        </label>
      </div>
      <label>Folders</label>
      {paths.map((p, i) => (
        <div key={i} className="path-row">
          <input value={p} onChange={(e) => setPaths(paths.map((x, j) => (j === i ? e.target.value : x)))} placeholder="/mnt/gdrive/Movies" />
          <button type="button" className="icon-btn glass" onClick={() => setPicking(i)} title="Browse"><Folder size={18} /></button>
          {paths.length > 1 && <button type="button" className="icon-btn glass" onClick={() => setPaths(paths.filter((_, j) => j !== i))}><X size={18} /></button>}
        </div>
      ))}
      <button type="button" className="btn glass sm" onClick={() => setPaths([...paths, ""])}><Plus size={16} /> Add folder</button>
      <label className="check">
        <input type="checkbox" checked={trust} onChange={(e) => setTrust(e.target.checked)} />
        <span>
          <b>Skip unchanged folders</b>
          <span className="muted"> Re-list a folder only when its modified time changes. This makes rescans much faster, but only turn it on if your mount updates folder times when files are added. Local disks and rclone mounts that are written to through the mount do; changes made directly on Google Drive may not show up. A full scan always lists everything, and one runs every night (4am by default). Libraries of 50,000+ files have this switched on automatically once.</span>
        </span>
      </label>
      {error && <div className="err">{error}</div>}
      <div className="form-actions">
        <button className="btn primary">{initial ? "Save" : "Add library"}</button>
        <button type="button" className="btn glass" onClick={onCancel}>Cancel</button>
      </div>
      {picking !== null && (
        <FolderPicker onClose={() => setPicking(null)} onPick={(p) => {
          setPaths(paths.map((x, j) => (j === picking ? p : x)));
          setPicking(null);
        }} />
      )}
    </form>
  );
}

function Libraries() {
  const { data: libs, reload } = useApi<Library[]>("/api/libraries");
  const [scans, setScans] = useState<ScanProgress[]>([]);
  const [editing, setEditing] = useState<Library | "new" | null>(null);

  useEffect(() => {
    let t: ReturnType<typeof setTimeout>;
    const poll = async () => {
      const s = await api.get<ScanProgress[]>("/api/scan/status").catch(() => []);
      setScans(s);
      const running = s.some((x) => x.running);
      if (running) reload();
      t = setTimeout(poll, running ? 1500 : 6000);
    };
    poll();
    return () => clearTimeout(t);
  }, [reload]);

  const scan = async (id: number, full: boolean) => {
    await api.post(`/api/libraries/${id}/scan${full ? "?full=1" : ""}`);
    setTimeout(() => api.get<ScanProgress[]>("/api/scan/status").then(setScans), 300);
  };
  const scanAll = async (full: boolean) => {
    await api.post(`/api/libraries/scan${full ? "?full=1" : ""}`);
    setTimeout(() => api.get<ScanProgress[]>("/api/scan/status").then(setScans), 300);
  };
  const anyRunning = scans.some((x) => x.running);
  const refreshMeta = async (l: Library) => {
    if (!confirm(`Re-fetch posters, descriptions and episode details for everything in "${l.name}" from TMDB? This runs in the background and can take a while for a big library.`)) return;
    await api.post(`/api/libraries/${l.id}/refresh`);
  };
  const remove = async (l: Library) => {
    if (!confirm(`Remove the library "${l.name}"? Your files are not touched.`)) return;
    await api.del(`/api/libraries/${l.id}`);
    api.invalidate();
    reload();
  };

  return (
    <section className="settings-section">
      <div className="section-head">
        <h2>Libraries</h2>
        {!editing && (
          <div className="row-btns">
            {(libs?.length ?? 0) > 1 && (
              <>
                <button className="btn glass sm" onClick={() => scanAll(false)} disabled={anyRunning} title="Scan every library, one after another"><Refresh size={16} /> Scan all</button>
                <button className="btn glass sm" onClick={() => scanAll(true)} disabled={anyRunning} title="Full scan of every library, one after another">Full scan all</button>
              </>
            )}
            <button className="btn primary sm" onClick={() => setEditing("new")}><Plus size={16} /> Add library</button>
          </div>
        )}
      </div>
      {editing && (
        <LibraryForm initial={editing === "new" ? undefined : editing} onCancel={() => setEditing(null)}
          onDone={() => { setEditing(null); reload(); }} />
      )}
      <div className="lib-list">
        {libs?.map((l) => {
          const s = scans.find((x) => x.libraryId === l.id);
          return (
            <div key={l.id} className="card lib-row">
              <div className="lib-row-main">
                <div className="t">{l.name} <span className="chip">{l.kind === "movies" ? "Movies" : "TV"}</span></div>
                <div className="muted small">{l.paths.join(" · ")}</div>
                <div className="stats">
                  <span><b>{l.count.toLocaleString()}</b> titles</span>
                  <span><b>{l.fileCount.toLocaleString()}</b> files</span>
                  <span><b>{fmtSize(l.totalSize)}</b></span>
                  {l.pendingMetadata > 0 && <span><b>{l.pendingMetadata.toLocaleString()}</b> awaiting metadata</span>}
                  <span>last scan {ago(l.lastScanAt)}</span>
                </div>
                {s?.running && (
                  <div className="scan-live">
                    <div className="spinner sm" /> Scanning: {s.dirs.toLocaleString()} folders listed
                    {s.dirsSkipped ? `, ${s.dirsSkipped.toLocaleString()} skipped` : ""}, {s.files.toLocaleString()} files,
                    {" "}+{s.added} new
                  </div>
                )}
                {!s?.running && s?.finished && (
                  <div className="muted small">
                    Last scan took {((Date.parse(s.finished) - Date.parse(s.started)) / 1000).toFixed(1)}s: +{s.added} new, {s.updated} changed, {s.removed} removed
                    {s.error && <span className="err-inline"> · {s.error}</span>}
                  </div>
                )}
              </div>
              <div className="lib-row-actions">
                <button className="btn glass sm" onClick={() => scan(l.id, false)} disabled={s?.running}><Refresh size={16} /> Scan</button>
                <button className="btn glass sm" onClick={() => scan(l.id, true)} disabled={s?.running} title="List every folder, ignoring remembered folder times">Full scan</button>
                <button className="btn glass sm" onClick={() => refreshMeta(l)} title="Re-fetch metadata for every title in this library">Refresh metadata</button>
                <button className="btn glass sm" onClick={() => setEditing(l)}>Edit</button>
                <button className="icon-btn glass" onClick={() => remove(l)} title="Remove library"><Trash size={18} /></button>
              </div>
            </div>
          );
        })}
      </div>
    </section>
  );
}

type PlexSignInT = {
  enabled: boolean; publish: boolean; publicUrl: string; seenUrl: string; published: string;
  status: { url: string; ok: boolean; error?: string; at: string };
};
type PlexT = { url: string; tokenSet: boolean; tokenFromEnv: boolean; pathMap: string; status: PlexStatus; sync: PlexSyncStatus; signIn: PlexSignInT };
type PlexSection = { title: string; type: string; locations: { plex: string; local: string; exists: boolean }[] };

function PlexImport() {
  const { data, setData } = useApi<PlexT>("/api/plex");
  const [url, setUrl] = useState<string>();
  const [token, setToken] = useState("");
  const [pathMap, setPathMap] = useState<string>();
  const [sections, setSections] = useState<PlexSection[]>();
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const running = data?.status.running;

  useEffect(() => {
    if (!running) return;
    const t = setInterval(() => api.get<PlexT>("/api/plex").then((d) => {
      setData(d);
      if (!d.status.running) api.invalidate();
    }), 1000);
    return () => clearInterval(t);
  }, [running, setData]);

  const save = async () => {
    const d = await api.put<PlexT>("/api/plex", { url: url ?? data?.url, token, pathMap: pathMap ?? data?.pathMap });
    setData(d);
    setToken("");
    return d;
  };
  const test = async () => {
    setBusy(true);
    setError(undefined);
    try {
      await save();
      setSections(await api.post<PlexSection[]>("/api/plex/test"));
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const start = async () => {
    setError(undefined);
    try {
      await api.post("/api/plex/import");
      setData(await api.get<PlexT>("/api/plex"));
    } catch (e) {
      setError((e as Error).message);
    }
  };
  const s = data?.status;
  const locs = sections?.flatMap((x) => x.locations) ?? [];
  const missing = locs.filter((l) => !l.exists).length;
  const noneFound = locs.length > 0 && missing === locs.length;

  return (
    <section className="settings-section">
      <h2>Import from Plex</h2>
      <div className="card form">
        <p className="muted small" style={{ margin: 0 }}>
          Brings over your Plex libraries, artwork, descriptions, watch history and intro/credit markers in one pass. Your files aren't read, so it's fast even on Google Drive. Afterwards Plexodus keeps itself up to date and Plex is optional.
        </p>
        <div className="form-row">
          <label>Plex server URL
            <input value={url ?? data?.url ?? ""} onChange={(e) => setUrl(e.target.value)} placeholder="http://127.0.0.1:32400" />
          </label>
          <label>Token
            <input type="password" value={token} onChange={(e) => setToken(e.target.value)}
              placeholder={data?.tokenSet ? (data.tokenFromEnv ? "set from PLEX_TOKEN" : "saved") : "X-Plex-Token"} />
          </label>
        </div>
        <label>Path mapping <span className="muted small">(only if Plex sees your files at different paths than Plexodus)</span>
          <input value={pathMap ?? data?.pathMap ?? ""} onChange={(e) => setPathMap(e.target.value)} placeholder="/data/media=/mnt/gdrive" />
        </label>
        {sections && (
          <div className="plex-sections">
            {sections.map((x) => (
              <div key={x.title} className="plex-sec">
                <div><b>{x.title}</b> <span className="chip">{x.type === "movie" ? "Movies" : "TV"}</span></div>
                {x.locations.map((l) => (
                  <div key={l.plex} className={`small ${l.exists ? "ok" : "err"}`}>
                    {l.exists ? "✓" : "✗ not found:"} {l.local}
                  </div>
                ))}
              </div>
            ))}
            {noneFound ? (
              <div className="err">None of these folders are visible here. Add a path mapping so Plex paths point at this machine's mount.</div>
            ) : missing > 0 && (
              <div className="muted small">{missing === 1 ? "1 folder isn't" : `${missing} folders aren't`} visible here. You can still import; items in {missing === 1 ? "it" : "them"} won't play until the folder is back.</div>
            )}
          </div>
        )}
        {error && <div className="err">{error}</div>}
        {s && (s.running || (s.finished && !s.finished.startsWith("0001"))) && (
          <div className={s.running ? "scan-live" : "muted small"}>
            {s.running && <div className="spinner sm" />}
            {s.phase}{s.section ? ` · ${s.section}` : ""}: {s.items.toLocaleString()} items, {s.files.toLocaleString()} files,{" "}
            {s.watched.toLocaleString()} watch records, {s.markers.toLocaleString()} markers
            {s.error && <span className="err-inline"> · {s.error}</span>}
          </div>
        )}
        <div className="form-actions">
          <button className="btn glass" onClick={test} disabled={busy || running}>{busy ? "Checking…" : "Save & check connection"}</button>
          <button className="btn primary" onClick={start} disabled={running || !sections || noneFound}>Import</button>
        </div>
      </div>
    </section>
  );
}

const setTime = (t?: string) => (t && !t.startsWith("0001") ? t : undefined);

function PlexSync() {
  const { data, setData } = useApi<PlexT>("/api/plex");
  const [error, setError] = useState<string>();
  // lastRun as it was when "Sync now" was pressed; null when not waiting.
  const [since, setSince] = useState<{ lastRun: string; at: number } | null>(null);
  const [, rerender] = useState(0);
  const sync = data?.sync;
  const busy = since !== null || !!sync?.running;

  // Poll while a sync is queued or running; otherwise just refresh the "x min ago" text.
  useEffect(() => {
    const t = setInterval(() => {
      if (!busy) return rerender((n) => n + 1);
      api.get<PlexT>("/api/plex").then((d) => {
        setData(d);
        const finished = !d.sync.running && (!since || (d.sync.lastRun ?? "") !== since.lastRun || Date.now() - since.at > 6 * 60_000);
        if (finished) {
          setSince(null);
          api.invalidate();
        }
      });
    }, busy ? 1000 : 30_000);
    return () => clearInterval(t);
  }, [busy, since, setData]);

  if (!data || !data.tokenSet || !data.url) return null;

  const run = async () => {
    setError(undefined);
    try {
      await api.post("/api/plex/sync");
      setSince({ lastRun: sync?.lastRun ?? "", at: Date.now() });
    } catch (e) {
      setError((e as Error).message);
    }
  };
  const toggle = async (on: boolean) => setData(await api.put<PlexT>("/api/plex", { syncEnabled: on }));
  const last = setTime(sync?.lastRun);
  const ok = setTime(sync?.lastOk);

  return (
    <section className="settings-section">
      <h2>Watch status from Plex</h2>
      <div className="card form">
        <p className="muted small" style={{ margin: 0 }}>
          Anything you watch in a Plex app shows up here as watched, with its resume point and on Up Next.
          Plexodus checks Plex every {sync?.intervalMinutes ?? 5} minutes. It only reads from Plex, and never replaces progress you made more recently in Plexodus.
        </p>
        <label className="check">
          <input type="checkbox" checked={!!sync?.enabled} onChange={(e) => toggle(e.target.checked)} />
          <span><b>Keep in sync automatically</b></span>
        </label>
        <div className={busy ? "scan-live" : "muted small"}>
          {busy && <div className="spinner sm" />}
          {busy ? "Syncing from Plex…" : last ? (
            <>
              Last sync {ago(last)}: {sync!.checked.toLocaleString()} recently played {sync!.checked === 1 ? "item" : "items"} checked,{" "}
              {sync!.updated.toLocaleString()} updated
              {sync!.error && <span className="err-inline"> · failed: {sync!.error}{ok ? ` (last good sync ${ago(ok)})` : ""}</span>}
            </>
          ) : sync?.enabled ? "First sync runs within a minute of the server starting." : "Not synced yet."}
        </div>
        {error && <div className="err">{error}</div>}
        <div className="form-actions">
          <button className="btn glass" onClick={run} disabled={busy || data.status.running}>Sync from Plex now</button>
        </div>
      </div>
    </section>
  );
}

function PlexSignInSettings() {
  const { data, setData } = useApi<PlexT>("/api/plex");
  const [addr, setAddr] = useState<string>();
  const [error, setError] = useState<string>();
  if (!data || !data.tokenSet || !data.url || !data.signIn) return null;
  const si = data.signIn;
  const put = async (body: Record<string, unknown>) => {
    setError(undefined);
    try {
      setData(await api.put<PlexT>("/api/plex", body));
      // The server updates Plex in the background; show the result shortly after.
      setTimeout(() => api.get<PlexT>("/api/plex").then(setData, () => {}), 3000);
    } catch (e) {
      setError((e as Error).message);
    }
  };
  const address = si.publicUrl || si.seenUrl;
  const st = si.status;
  return (
    <section className="settings-section">
      <h2>Sign in with Plex</h2>
      <div className="card form">
        <p className="muted small" style={{ margin: 0 }}>
          Anyone your Plex server is shared with can sign in with their Plex account, with no password to hand out.
          They get their own account and see the libraries you share with them in Plex. If you stop sharing with them in Plex,
          they lose access here within the hour.
        </p>
        <label className="check">
          <input type="checkbox" checked={si.enabled} onChange={(e) => put({ signInEnabled: e.target.checked })} />
          <span><b>Allow Sign in with Plex</b></span>
        </label>
        <label className="check">
          <input type="checkbox" checked={si.publish} disabled={!si.enabled} onChange={(e) => put({ publish: e.target.checked })} />
          <span>
            <b>Let the Plexodus app find this server</b>
            <span className="muted small"> Adds this server's address to your Plex server's "Custom server access URLs", which Plex shares with
              your friends. The app then finds this server after they sign in with Plex, so nobody types an address.</span>
          </span>
        </label>
        <label>Public address of this server
          <input value={addr ?? si.publicUrl} placeholder={si.seenUrl || window.location.origin}
            onChange={(e) => setAddr(e.target.value)} />
        </label>
        <div className="muted small">
          {!si.publicUrl && si.seenUrl && <>Using {si.seenUrl}, the address you opened Plexodus at. </>}
          {si.enabled && si.publish && (st?.error
            ? <span className="err-inline">Couldn't update Plex: {st.error}</span>
            : si.published ? <>Published to Plex as {si.published}.</>
            : address ? "Publishing to Plex…" : "Set the address your friends use to reach this server.")}
        </div>
        {error && <div className="err">{error}</div>}
        <div className="form-actions">
          <button className="btn glass" disabled={addr === undefined || addr === si.publicUrl}
            onClick={() => put({ publicUrl: addr ?? "" }).then(() => setAddr(undefined))}>Save address</button>
        </div>
      </div>
    </section>
  );
}

type SettingsT = {
  tmdbKeySet: boolean; tmdbKeyMasked: string; tmdbKeyFromEnv: boolean; apiKey: string;
  anthropicKeySet: boolean; anthropicKeyMasked: string; anthropicKeyFromEnv: boolean; askModel: string;
  detectMarkers: boolean;
  autoUpdate?: AutoUpdateT;
};

type AutoUpdateT = {
  enabled: boolean; fromEnv: boolean; version: string; installed: boolean; pending?: boolean;
  last?: { state: string; message?: string; checkedAt?: number; updatedAt?: number; version?: string };
};

function Updates() {
  const { data, setData, reload } = useApi<SettingsT>("/api/settings");
  const [busy, setBusy] = useState(false);
  const u = data?.autoUpdate;
  // While an "Update now" is pending, check back until the updater has run
  // (the server restarts in between, so a failed fetch is expected).
  useEffect(() => {
    if (!u?.pending && !busy) return;
    const t = setInterval(reload, 4000);
    return () => clearInterval(t);
  }, [u?.pending, busy, reload]);
  useEffect(() => {
    if (busy && u && !u.pending) setBusy(false);
  }, [busy, u]);
  if (!u) return null;
  const put = async (body: object) => setData(await api.put<SettingsT>("/api/settings", body));
  const updateNow = async () => {
    if (!confirm("Install the latest version now? Plexodus restarts, so anyone watching will see their player pause for a few seconds.")) return;
    setBusy(true);
    await put({ updateNow: true });
  };
  return (
    <section className="settings-section">
      <h2>Updates</h2>
      <div className="card form">
        <p className="muted small" style={{ margin: 0 }}>
          Running version <b>{u.version}</b>.
          {u.installed
            ? " Plexodus checks for a new release every 15 minutes and installs it by itself, waiting until nobody is watching so no film gets cut off."
            : " This server was installed without the automatic updater. Run the install command once more on the server to add it:"}
        </p>
        {!u.installed && <pre className="code">curl -fsSL https://github.com/sfxhewitt/plexodus/releases/download/latest/install.sh | sudo bash</pre>}
        {u.installed && (
          <>
            <label className="check">
              <input type="checkbox" checked={u.enabled} disabled={u.fromEnv}
                onChange={(e) => put({ autoUpdate: e.target.checked })} />
              <span><b>Update automatically</b>{u.fromEnv && <span className="muted"> (turned off by PLEXODUS_AUTO_UPDATE in the server's settings file)</span>}</span>
            </label>
            {u.last && (
              <p className="muted small" style={{ margin: 0 }}>
                {u.last.message} Last checked {ago(u.last.checkedAt)}
                {u.last.updatedAt ? `, last updated ${ago(u.last.updatedAt)}` : ""}.
              </p>
            )}
            <div className="form-actions">
              <button className="btn glass" disabled={busy || u.pending} onClick={updateNow}>
                {busy || u.pending ? "Updating…" : "Update now"}
              </button>
            </div>
          </>
        )}
      </div>
    </section>
  );
}

function AskSettings() {
  const { data, setData } = useApi<SettingsT>("/api/settings");
  const [key, setKey] = useState("");
  const [msg, setMsg] = useState<string>();
  const save = async (k: string) => {
    setData(await api.put<SettingsT>("/api/settings", { anthropicKey: k }));
    setKey("");
    setMsg(k ? "Saved. Try typing a request in the search box and pressing Enter." : "Key removed. Search falls back to basic mode.");
  };
  const stored = data?.anthropicKeySet && !data.anthropicKeyFromEnv;
  return (
    <section className="settings-section">
      <h2>Plain-English search</h2>
      <form className="card form" onSubmit={(e: FormEvent) => (e.preventDefault(), save(key))}>
        <label>Anthropic API key
          <input value={key} onChange={(e) => setKey(e.target.value)} autoComplete="off"
            placeholder={data?.anthropicKeySet ? `${data.anthropicKeyMasked}${data.anthropicKeyFromEnv ? " (from environment)" : ""}` : "sk-ant-…"} />
        </label>
        <p className="muted small">
          Type a request like “90s heist films under two hours I haven’t seen” or “something like Severance” in search and press Enter.
          With a key, Claude ({data?.askModel || "claude"}) turns it into a library search and suggests titles; repeated searches are cached.
          Without one, a built-in parser handles decades, lengths, genres and “haven’t seen”. Keys come from console.anthropic.com → API keys.
        </p>
        {msg && <div className="ok">{msg}</div>}
        <div className="form-actions">
          {stored && <button type="button" className="btn glass" onClick={() => save("")}>Remove key</button>}
          <button className="btn primary" disabled={!key.trim()}>Save key</button>
        </div>
      </form>
    </section>
  );
}

function Metadata() {
  const { data, setData } = useApi<SettingsT>("/api/settings");
  const [key, setKey] = useState("");
  const [msg, setMsg] = useState<string>();
  const save = async (e: FormEvent) => {
    e.preventDefault();
    const s = await api.put<SettingsT>("/api/settings", { tmdbKey: key });
    setData(s);
    setKey("");
    setMsg("Saved. Matching runs in the background.");
  };
  const regen = async () => {
    if (!confirm("Generate a new API key? Anything using the old key will stop working.")) return;
    setData(await api.put<SettingsT>("/api/settings", { regenerateApiKey: true }));
  };
  const origin = window.location.origin;
  return (
    <>
      <section className="settings-section">
        <h2>Metadata</h2>
        <form className="card form" onSubmit={save}>
          <label>TMDB API key
            <input value={key} onChange={(e) => setKey(e.target.value)}
              placeholder={data?.tmdbKeySet ? `${data.tmdbKeyMasked}${data.tmdbKeyFromEnv ? " (from environment)" : ""}` : "Paste your v3 API key or v4 read token"} />
          </label>
          <p className="muted small">Posters, backdrops, descriptions and episode titles come from TMDB. You can get a free key at themoviedb.org → Settings → API.</p>
          {msg && <div className="ok">{msg}</div>}
          <div className="form-actions"><button className="btn primary" disabled={!key}>Save key</button></div>
        </form>
      </section>
      <AskSettings />
      <section className="settings-section">
        <h2>Skip buttons</h2>
        <div className="card form">
          <p className="muted small" style={{ margin: 0 }}>
            Intro and credits markers come from Plex where it has them. Plexodus also finds recaps, and intros or credits Plex didn't mark,
            when an episode starts playing: first from chapter names, then by comparing its sound with the episodes around it.
            This reads the previous two episodes once (usually already cached if you watched them) and gets the next episode ready while you finish this one.
            Nothing is read during library scans.
          </p>
          <label className="check">
            <input type="checkbox" checked={data?.detectMarkers ?? true}
              onChange={async (e) => setData(await api.put<SettingsT>("/api/settings", { detectMarkers: e.target.checked }))} />
            <span><b>Find recaps, intros and credits on first play</b></span>
          </label>
        </div>
      </section>
      <section className="settings-section">
        <h2>Integrations</h2>
        <div className="card form">
          <label>API key</label>
          <div className="path-row">
            <input readOnly value={data?.apiKey || ""} onFocus={(e) => e.target.select()} />
            <button className="btn glass sm" onClick={regen}>Regenerate</button>
          </div>
          <p className="muted small">
            Libraries are rescanned automatically every 15 minutes. Scripts or tools can also ask for an immediate scan of a single folder:
          </p>
          <pre className="code">curl -X POST "{origin}/api/scan?apikey={data?.apiKey || "KEY"}&path=/mnt/media/Movies/Some%20Movie%20(2024)"</pre>
        </div>
      </section>
    </>
  );
}

function LibraryPicker({ all, libs, onChange }: { all: boolean; libs: number[]; onChange: (all: boolean, libs: number[]) => void }) {
  const { data: libraries } = useApi<Library[]>("/api/libraries");
  const toggle = (id: number) => onChange(false, libs.includes(id) ? libs.filter((x) => x !== id) : [...libs, id]);
  return (
    <div className="lib-picker">
      <button type="button" className={`pick ${all ? "on" : ""}`} onClick={() => onChange(!all, libs)}>All libraries, including future ones</button>
      {libraries?.map((l) => (
        <button type="button" key={l.id} className={`pick ${!all && libs.includes(l.id) ? "on" : ""}`} disabled={all} onClick={() => toggle(l.id)}>
          {l.name}
        </button>
      ))}
    </div>
  );
}

function AccessModal({ u, onClose, onSaved }: { u: AdminUser; onClose: () => void; onSaved: () => void }) {
  const [all, setAll] = useState(u.allLibraries);
  const [libs, setLibs] = useState(u.libraries);
  const [followPlex, setFollowPlex] = useState(u.followPlex);
  const [error, setError] = useState<string>();
  const save = async () => {
    setError(undefined);
    try {
      await api.put(`/api/users/${u.id}/access`, { allLibraries: all, libraries: libs, followPlex });
      onSaved();
    } catch (e) {
      setError((e as Error).message);
    }
  };
  return (
    <div className="modal-back" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <h2>Libraries {u.username} can see</h2>
          <button className="icon-btn" onClick={onClose} aria-label="Close"><X /></button>
        </div>
        {u.plexUsername && (
          <div className="lib-picker">
            <button type="button" className={`pick ${followPlex ? "on" : ""}`} onClick={() => setFollowPlex(true)}>Same as their Plex share</button>
            <button type="button" className={`pick ${!followPlex ? "on" : ""}`} onClick={() => setFollowPlex(false)}>Choose in Plexodus</button>
          </div>
        )}
        {u.plexUsername && (
          <p className="muted small" style={{ margin: "8px 0" }}>
            {followPlex
              ? `${u.plexUsername} sees the libraries you share with them in Plex, checked every hour.`
              : "Your choice below sticks, whatever you share in Plex. Removing them from Plex sharing altogether still signs them out."}
          </p>
        )}
        {!followPlex && <LibraryPicker all={all} libs={libs} onChange={(a, l) => { setAll(a); setLibs(l); }} />}
        {error && <div className="err">{error}</div>}
        <div className="modal-foot"><button className="btn primary" onClick={save} disabled={!followPlex && !all && libs.length === 0}>Save</button></div>
      </div>
    </div>
  );
}

function Invites() {
  const { data: invites, reload } = useApi<Invite[]>("/api/invites");
  const { data: libraries } = useApi<Library[]>("/api/libraries");
  const [all, setAll] = useState(false);
  const [libs, setLibs] = useState<number[]>([]);
  const [note, setNote] = useState("");
  const [days, setDays] = useState(7);
  const [anyone, setAnyone] = useState(false);
  const [made, setMade] = useState<{ url: string; code?: string; codeError?: string }>();
  const [error, setError] = useState<string>();
  const create = async () => {
    setError(undefined);
    try {
      const r = await api.post<{ url: string; code?: string; codeError?: string }>("/api/invites",
        { allLibraries: all, libraries: libs, note, days, maxUses: anyone ? 0 : 1 });
      setMade(r);
      setNote("");
      reload();
    } catch (e) {
      setError((e as Error).message);
    }
  };
  const revoke = async (t: string) => {
    await api.del(`/api/invites/${t}`);
    reload();
  };
  const names = (inv: Invite) => inv.allLibraries ? "All libraries" : inv.libraries.map((id) => libraries?.find((l) => l.id === id)?.name).filter(Boolean).join(", ");
  return (
    <section className="settings-section">
      <h2>Share libraries</h2>
      <div className="card form">
        <p className="muted small" style={{ margin: 0 }}>
          Invite a friend: they type the invite code into the Plexodus app (no server address needed) or open the link. They choose their own
          username and password, see only the libraries you pick, and get their own watch history and profiles.
        </p>
        <LibraryPicker all={all} libs={libs} onChange={(a, l) => { setAll(a); setLibs(l); }} />
        <div className="lib-picker">
          {[1, 7, 30, 365].map((d) => (
            <button key={d} type="button" className={`pick ${days === d ? "on" : ""}`} onClick={() => setDays(d)}>
              {d === 1 ? "1 day" : d === 365 ? "1 year" : `${d} days`}
            </button>
          ))}
          <button type="button" className={`pick ${!anyone ? "on" : ""}`} onClick={() => setAnyone(false)}>One person</button>
          <button type="button" className={`pick ${anyone ? "on" : ""}`} onClick={() => setAnyone(true)}
            title="Anyone with the code can make an account until it expires or you revoke it, e.g. for an app store reviewer">Anyone with the code</button>
        </div>
        <div className="path-row">
          <input value={note} onChange={(e) => setNote(e.target.value)} placeholder="Who is it for? (optional note)" />
          <button className="btn primary" onClick={create} disabled={!all && libs.length === 0}>Create invite</button>
        </div>
        {error && <div className="err">{error}</div>}
        {made && (
          <>
            {made.code && !made.codeError && (
              <div className="invite-code">
                <span className="muted small">Invite code</span>
                <b>{made.code}</b>
                <button className="btn glass sm" onClick={() => navigator.clipboard?.writeText(made.code!)}><Copy size={16} /> Copy</button>
              </div>
            )}
            {made.codeError && <div className="muted small">{made.codeError}</div>}
            <div className="path-row">
              <input readOnly value={made.url} onFocus={(e) => e.target.select()} />
              <button className="btn glass sm" onClick={() => navigator.clipboard?.writeText(made.url)}><Copy size={16} /> Copy link</button>
            </div>
          </>
        )}
        {invites && invites.length > 0 && (
          <div className="invite-list">
            {invites.map((inv) => (
              <div key={inv.token} className="invite-row">
                <div>
                  <div className="t">
                    {inv.code && <span className="chip">{inv.code}</span>} {inv.note || "Invite"} <span className="muted small">· {names(inv)}</span>
                  </div>
                  <div className="muted small">
                    {inv.usedBy ? `Accepted by ${inv.usedBy}` : `Expires ${new Date(inv.expiresAt * 1000).toLocaleDateString()}`}
                    {inv.maxUses === 0 && ` · anyone with the code · used ${inv.uses} ${inv.uses === 1 ? "time" : "times"}`}
                  </div>
                </div>
                {!inv.usedBy && <button className="btn glass sm" onClick={() => revoke(inv.token)}>Revoke</button>}
              </div>
            ))}
          </div>
        )}
      </div>
    </section>
  );
}

function Users() {
  const { user } = useUser();
  const { data: users, reload } = useApi<AdminUser[]>("/api/users");
  const { data: libraries } = useApi<Library[]>("/api/libraries");
  const [name, setName] = useState("");
  const [pass, setPass] = useState("");
  const [admin, setAdmin] = useState(false);
  const [error, setError] = useState<string>();
  const [editing, setEditing] = useState<AdminUser>();
  const add = async (e: FormEvent) => {
    e.preventDefault();
    setError(undefined);
    try {
      const u = await api.post<User>("/api/users", { username: name, password: pass, isAdmin: admin });
      setName("");
      setPass("");
      setAdmin(false);
      reload();
      // Pick what a new friend can see straight away.
      if (!u.isAdmin) setEditing({ ...u, followPlex: false });
    } catch (err) {
      setError((err as Error).message);
    }
  };
  const del = async (u: User) => {
    if (!confirm(`Delete ${u.username}? Their watch history is removed too.`)) return;
    await api.del(`/api/users/${u.id}`);
    reload();
  };
  const reset = async (u: User) => {
    const p = prompt(`New password for ${u.username}`);
    if (p) await api.put(`/api/users/${u.id}/password`, { password: p }).catch((e) => alert(e.message));
  };
  const access = (u: AdminUser) => {
    let s: string;
    if (u.isAdmin || u.allLibraries) s = "All libraries";
    else if (!u.libraries.length) s = "No libraries";
    else s = u.libraries.map((id) => libraries?.find((l) => l.id === id)?.name).filter(Boolean).join(", ");
    if (u.plexUsername && !u.isAdmin) s += u.followPlex ? " · from Plex sharing" : " · chosen in Plexodus";
    return s;
  };
  return (
    <section className="settings-section">
      <h2>Users</h2>
      <div className="card">
        {users?.map((u) => (
          <div key={u.id} className="user-row">
            <span className="avatar static"><span>{u.username[0].toUpperCase()}</span></span>
            <div className="user-main">
              <span className="t">
                {u.username} {u.isAdmin && <span className="chip">admin</span>}{" "}
                {u.plexUsername && <span className="chip" title={`Plex account: ${u.plexUsername}`}>Plex</span>}
              </span>
              <span className="muted small">{access(u)}</span>
            </div>
            <span className="spacer" />
            {!u.isAdmin && <button className="btn glass sm" onClick={() => setEditing(u)}>Libraries</button>}
            <button className="btn glass sm" onClick={() => reset(u)}>Reset password</button>
            {u.id !== user?.id && <button className="icon-btn glass" onClick={() => del(u)}><Trash size={18} /></button>}
          </div>
        ))}
        <form className="user-add" onSubmit={add}>
          <input placeholder="Username" value={name} onChange={(e) => setName(e.target.value)} />
          <input placeholder="Password" type="password" value={pass} onChange={(e) => setPass(e.target.value)} />
          <label className="check inline"><input type="checkbox" checked={admin} onChange={(e) => setAdmin(e.target.checked)} /> Admin</label>
          <button className="btn primary sm" disabled={!name || !pass}><Plus size={16} /> Add user</button>
        </form>
        {error && <div className="err">{error}</div>}
      </div>
      {editing && <AccessModal u={editing} onClose={() => setEditing(undefined)} onSaved={() => { setEditing(undefined); reload(); }} />}
    </section>
  );
}

export default function Settings() {
  return (
    <div className="settings page">
      <h1>Settings</h1>
      <Libraries />
      <PlexImport />
      <PlexSync />
      <PlexSignInSettings />
      <Metadata />
      <Updates />
      <Invites />
      <Users />
    </div>
  );
}
