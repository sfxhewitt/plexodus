import { FormEvent, useEffect, useState } from "react";
import { api, fmtSize, Library, ScanProgress, User } from "../api";
import { Folder, Plus, Refresh, Trash, X } from "../components/Icons";
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
        {!editing && <button className="btn primary sm" onClick={() => setEditing("new")}><Plus size={16} /> Add library</button>}
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

function Users() {
  const { user } = useUser();
  const { data: users, reload } = useApi<User[]>("/api/users");
  const [name, setName] = useState("");
  const [pass, setPass] = useState("");
  const [admin, setAdmin] = useState(false);
  const [error, setError] = useState<string>();
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
  return (
    <section className="settings-section">
      <h2>Users</h2>
      <div className="card">
        {users?.map((u) => (
          <div key={u.id} className="user-row">
            <span className="avatar static"><span>{u.username[0].toUpperCase()}</span></span>
            <span className="t">{u.username}</span>
            {u.isAdmin && <span className="chip">admin</span>}
            <span className="spacer" />
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
    </section>
  );
}

export default function Settings() {
  return (
    <div className="settings page">
      <h1>Settings</h1>
      <Libraries />
      <Metadata />
      <Users />
    </div>
  );
}
