import { FormEvent, useEffect, useState } from "react";
import { api, fmtSize, Invite, Library, PlexStatus, ScanProgress, User } from "../api";
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
          <span className="muted"> Re-list a folder only when its modified time changes. This makes rescans much faster, but only turn it on if your mount updates folder times when files are added. Local disks and rclone mounts that are written to through the mount do; changes made directly on Google Drive may not show up. A full scan always lists everything.</span>
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

type PlexT = { url: string; tokenSet: boolean; tokenFromEnv: boolean; pathMap: string; status: PlexStatus };
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

type SettingsT = { tmdbKeySet: boolean; tmdbKeyMasked: string; tmdbKeyFromEnv: boolean; apiKey: string };

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

function AccessModal({ u, onClose, onSaved }: { u: User; onClose: () => void; onSaved: () => void }) {
  const [all, setAll] = useState(u.allLibraries);
  const [libs, setLibs] = useState(u.libraries);
  const save = async () => {
    await api.put(`/api/users/${u.id}/access`, { allLibraries: all, libraries: libs });
    onSaved();
  };
  return (
    <div className="modal-back" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <h2>Libraries {u.username} can see</h2>
          <button className="icon-btn" onClick={onClose} aria-label="Close"><X /></button>
        </div>
        <LibraryPicker all={all} libs={libs} onChange={(a, l) => { setAll(a); setLibs(l); }} />
        <div className="modal-foot"><button className="btn primary" onClick={save} disabled={!all && libs.length === 0}>Save</button></div>
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
  const [link, setLink] = useState<string>();
  const [error, setError] = useState<string>();
  const create = async () => {
    setError(undefined);
    try {
      const r = await api.post<{ url: string }>("/api/invites", { allLibraries: all, libraries: libs, note });
      setLink(r.url);
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
        <p className="muted small" style={{ margin: 0 }}>Create an invite link and send it to a friend. They choose their own username and password, see only the libraries you pick, and get their own watch history. Links work once and expire after 7 days.</p>
        <LibraryPicker all={all} libs={libs} onChange={(a, l) => { setAll(a); setLibs(l); }} />
        <div className="path-row">
          <input value={note} onChange={(e) => setNote(e.target.value)} placeholder="Who is it for? (optional note)" />
          <button className="btn primary" onClick={create} disabled={!all && libs.length === 0}>Create invite link</button>
        </div>
        {error && <div className="err">{error}</div>}
        {link && (
          <div className="path-row">
            <input readOnly value={link} onFocus={(e) => e.target.select()} />
            <button className="btn glass sm" onClick={() => navigator.clipboard?.writeText(link)}><Copy size={16} /> Copy</button>
          </div>
        )}
        {invites && invites.length > 0 && (
          <div className="invite-list">
            {invites.map((inv) => (
              <div key={inv.token} className="invite-row">
                <div>
                  <div className="t">{inv.note || "Invite"} <span className="muted small">· {names(inv)}</span></div>
                  <div className="muted small">{inv.usedBy ? `Accepted by ${inv.usedBy}` : `Expires ${new Date(inv.expiresAt * 1000).toLocaleDateString()}`}</div>
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
  const { data: users, reload } = useApi<User[]>("/api/users");
  const { data: libraries } = useApi<Library[]>("/api/libraries");
  const [name, setName] = useState("");
  const [pass, setPass] = useState("");
  const [admin, setAdmin] = useState(false);
  const [error, setError] = useState<string>();
  const [editing, setEditing] = useState<User>();
  const add = async (e: FormEvent) => {
    e.preventDefault();
    setError(undefined);
    try {
      await api.post("/api/users", { username: name, password: pass, isAdmin: admin });
      setName("");
      setPass("");
      setAdmin(false);
      reload();
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
  const access = (u: User) => {
    if (u.isAdmin || u.allLibraries) return "All libraries";
    if (!u.libraries.length) return "No libraries";
    return u.libraries.map((id) => libraries?.find((l) => l.id === id)?.name).filter(Boolean).join(", ");
  };
  return (
    <section className="settings-section">
      <h2>Users</h2>
      <div className="card">
        {users?.map((u) => (
          <div key={u.id} className="user-row">
            <span className="avatar static"><span>{u.username[0].toUpperCase()}</span></span>
            <div className="user-main">
              <span className="t">{u.username} {u.isAdmin && <span className="chip">admin</span>}</span>
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
      <Metadata />
      <Invites />
      <Users />
    </div>
  );
}
