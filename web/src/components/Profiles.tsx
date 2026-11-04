// "Who's watching?" and profile management for the web interface.
import { FormEvent, useEffect, useState } from "react";
import { User } from "../api";
import {
  AVATARS, COLORS, colorOf, createProfile, deleteProfile, initialOf, loadProfiles, needsPin, Profile, ProfileInput,
  ProfilesInfo, switchProfile, updateProfile,
} from "../profiles";
import { Lock, Pencil, Plus, Trash, X } from "./Icons";

export function ProfileAvatar({ p, size = 120 }: { p: { id: number; name?: string; avatar?: string; color?: string }; size?: number }) {
  const c = colorOf(p);
  return (
    <span className="pf-avatar" style={{ width: size, height: size, fontSize: size * 0.46, "--pf": c } as React.CSSProperties}>
      {p.avatar || <b style={{ fontSize: size * 0.4 }}>{initialOf(p.name)}</b>}
    </span>
  );
}

/** Four-digit PIN entry. */
function PinPrompt({ p, onCancel, onDone }: { p: Profile; onCancel: () => void; onDone: (u: User) => void }) {
  const [pin, setPin] = useState("");
  const [err, setErr] = useState<string>();
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (pin.length < 4 || busy) return;
    setBusy(true);
    switchProfile(p.id, pin).then(onDone, (e: Error) => {
      setErr(e.message);
      setPin("");
      setBusy(false);
    });
  }, [pin]);
  return (
    <div className="modal-back" onClick={onCancel}>
      <div className="modal pf-pin" onClick={(e) => e.stopPropagation()}>
        <ProfileAvatar p={p} size={72} />
        <h2>Enter {p.name}'s PIN</h2>
        <input autoFocus inputMode="numeric" autoComplete="off" type="password" maxLength={4} value={pin} aria-label="PIN"
          onChange={(e) => setPin(e.target.value.replace(/\D/g, "").slice(0, 4))} onKeyDown={(e) => e.key === "Escape" && onCancel()} />
        <div className="pf-dots">{[0, 1, 2, 3].map((i) => <span key={i} className={i < pin.length ? "on" : ""} />)}</div>
        {err && <div className="err">{err}</div>}
        <button className="btn glass sm" onClick={onCancel}>Cancel</button>
      </div>
    </div>
  );
}

export function ProfileEditor({ p, info, onClose, onSaved }: { p?: Profile; info: ProfilesInfo; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(p?.name || "");
  const [avatar, setAvatar] = useState(p?.avatar ?? AVATARS[info.profiles.length % AVATARS.length]);
  const [color, setColor] = useState(p?.color || Object.keys(COLORS)[(info.profiles.length * 3) % 8]);
  const [kids, setKids] = useState(p?.kids || false);
  const [maxAge, setMaxAge] = useState(p?.maxAge ?? 8);
  const [pin, setPin] = useState("");
  const [removePin, setRemovePin] = useState(false);
  const [err, setErr] = useState<string>();
  const [busy, setBusy] = useState(false);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (pin && pin.length !== 4) return setErr("The PIN must be 4 digits.");
    const body: ProfileInput = { name, avatar, color };
    if (!p?.isAccount) Object.assign(body, { kids, maxAge });
    if (pin) body.pin = pin;
    else if (removePin) body.pin = "";
    setBusy(true);
    try {
      if (p) await updateProfile(p.id, body);
      else await createProfile(body);
      onSaved();
    } catch (e) {
      setErr((e as Error).message);
      setBusy(false);
    }
  };
  const remove = async () => {
    if (!p || !confirm(`Delete ${p.name}? Their watch history and watchlist go with it.`)) return;
    await deleteProfile(p.id).then(onSaved, (e: Error) => setErr(e.message));
  };

  return (
    <div className="modal-back" onClick={onClose}>
      <form className="modal pf-editor" onClick={(e) => e.stopPropagation()} onSubmit={save}>
        <div className="modal-head">
          <h2>{p ? `Edit ${p.name}` : "Add a profile"}</h2>
          <button type="button" className="icon-btn" onClick={onClose} aria-label="Close"><X /></button>
        </div>
        <div className="pf-editor-top">
          <ProfileAvatar p={{ id: p?.id ?? 0, name, avatar, color }} size={84} />
          <input autoFocus placeholder="Name" maxLength={24} value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <div className="pf-label">Picture</div>
        <div className="pf-choices">
          <button type="button" className={`pf-choice ${avatar === "" ? "on" : ""}`} onClick={() => setAvatar("")}>{initialOf(name)}</button>
          {AVATARS.map((a) => (
            <button type="button" key={a} className={`pf-choice ${avatar === a ? "on" : ""}`} onClick={() => setAvatar(a)}>{a}</button>
          ))}
        </div>
        <div className="pf-label">Colour</div>
        <div className="pf-choices">
          {Object.entries(COLORS).map(([k, v]) => (
            <button type="button" key={k} aria-label={k} className={`pf-swatch ${color === k ? "on" : ""}`} style={{ background: v }} onClick={() => setColor(k)} />
          ))}
        </div>
        {!p?.isAccount && (
          <>
            <label className="pf-check">
              <input type="checkbox" checked={kids} onChange={(e) => setKids(e.target.checked)} />
              <span><b>Kids profile</b><span className="muted small"> Only shows titles rated for this age. Unrated titles are hidden.</span></span>
            </label>
            {kids && (
              <div className="pf-levels">
                {info.levels.map((l) => (
                  <button type="button" key={l.maxAge} className={`pf-level ${maxAge === l.maxAge ? "on" : ""}`} onClick={() => setMaxAge(l.maxAge)}>
                    <b>{l.label}</b><span>{l.detail}</span>
                  </button>
                ))}
              </div>
            )}
          </>
        )}
        <div className="pf-label">PIN {p?.hasPin && !removePin ? "(set)" : ""}</div>
        <div className="pf-pin-row">
          <input inputMode="numeric" type="password" autoComplete="new-password" maxLength={4} placeholder={p?.hasPin ? "New 4-digit PIN" : "Optional 4-digit PIN"}
            value={pin} onChange={(e) => setPin(e.target.value.replace(/\D/g, "").slice(0, 4))} />
          {p?.hasPin && !pin && (
            <button type="button" className="btn glass sm" onClick={() => setRemovePin(!removePin)}>{removePin ? "Keep PIN" : "Remove PIN"}</button>
          )}
        </div>
        <p className="muted small" style={{ margin: 0 }}>
          {p?.isAccount
            ? "A PIN on your main profile stops other profiles (like the kids) from switching to it."
            : "Anyone switching to this profile needs the PIN, except from your main profile."}
        </p>
        {err && <div className="err">{err}</div>}
        <div className="modal-foot pf-foot">
          {p && !p.isAccount && <button type="button" className="btn glass sm" onClick={remove}><Trash size={16} /> Delete</button>}
          <span style={{ flex: 1 }} />
          <button className="btn primary" disabled={busy || !name.trim()}>{p ? "Save" : "Add profile"}</button>
        </div>
      </form>
    </div>
  );
}

/** Full-screen "Who's watching?". */
export function WhoIsWatching({ onPicked, onClose, manage: startManage = false }: {
  onPicked: (u: User) => void; onClose?: () => void; manage?: boolean;
}) {
  const [info, setInfo] = useState<ProfilesInfo>();
  const [err, setErr] = useState<string>();
  const [pinFor, setPinFor] = useState<Profile>();
  const [manage, setManage] = useState(startManage);
  const [editing, setEditing] = useState<Profile | "new">();
  const reload = () => loadProfiles().then(setInfo, (e: Error) => setErr(e.message));
  useEffect(() => {
    reload();
  }, []);

  const pick = (p: Profile) => {
    if (!info) return;
    if (manage) return setEditing(p);
    if (needsPin(p, info)) return setPinFor(p);
    switchProfile(p.id).then(onPicked, (e: Error) => setErr(e.message));
  };

  return (
    <div className="pf-screen">
      <div className="pf-glow" />
      <h1>{manage ? "Manage profiles" : "Who's watching?"}</h1>
      {err && <div className="err">{err}</div>}
      {!info && !err && <div className="spinner" />}
      {info && (
        <div className="pf-grid">
          {info.profiles.map((p) => (
            <button key={p.id} className={`pf-tile ${manage ? "editing" : ""}`} onClick={() => pick(p)}>
              <span className="pf-tile-art">
                <ProfileAvatar p={p} />
                {manage && <span className="pf-edit"><Pencil size={30} /></span>}
              </span>
              <span className="pf-name">{p.name}</span>
              <span className="pf-tags">
                {p.kids && <span className="chip hi">Kids</span>}
                {p.hasPin && <Lock size={14} />}
              </span>
            </button>
          ))}
          {info.canManage && info.profiles.length <= info.max && (
            <button className="pf-tile" onClick={() => setEditing("new")}>
              <span className="pf-tile-art"><span className="pf-avatar pf-add"><Plus size={44} /></span></span>
              <span className="pf-name muted">Add profile</span>
            </button>
          )}
        </div>
      )}
      <div className="pf-actions">
        {info?.canManage && (
          <button className="btn glass" onClick={() => setManage(!manage)}>{manage ? "Done" : "Manage profiles"}</button>
        )}
        {onClose && !manage && <button className="btn glass" onClick={onClose}>Back</button>}
      </div>
      {pinFor && <PinPrompt p={pinFor} onCancel={() => setPinFor(undefined)} onDone={onPicked} />}
      {editing && info && (
        <ProfileEditor p={editing === "new" ? undefined : editing} info={info} onClose={() => setEditing(undefined)}
          onSaved={() => (setEditing(undefined), reload())} />
      )}
    </div>
  );
}
