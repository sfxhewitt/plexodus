// "Who's watching?", PIN pad and profile editing for the TV interface.
// Everything is a plain focusable (data-f) so the D-pad engine drives it.
import { useEffect, useRef, useState } from "react";
import { User } from "../api";
import { Check, Lock, Pencil, Plus, Trash, X } from "../components/Icons";
import {
  AVATARS, COLORS, colorOf, createProfile, deleteProfile, initialOf, loadProfiles, needsPin, Profile, ProfileInput,
  ProfilesInfo, switchProfile, updateProfile,
} from "../profiles";
import { Spinner, TvButton } from "./components";
import { focusInitial, focusKey, onBack } from "./focus";

export function TvAvatar({ p, className }: { p: { id: number; name?: string; avatar?: string; color?: string }; className?: string }) {
  return (
    <span className={`tv-pf-avatar ${className || ""}`} style={{ "--pf": colorOf(p) } as React.CSSProperties}>
      {p.avatar ? <span className="e">{p.avatar}</span> : <b>{initialOf(p.name)}</b>}
    </span>
  );
}

const PAD = ["1", "2", "3", "4", "5", "6", "7", "8", "9", "back", "0", "cancel"];

/** On-screen number pad; the remote's number keys work too. */
function PinPad({ title, sub, onCancel, onEnter }: {
  title: string; sub?: string; onCancel: () => void; onEnter: (pin: string) => Promise<string | void>;
}) {
  const [pin, setPin] = useState("");
  const [err, setErr] = useState<string>();
  const busy = useRef(false);
  useEffect(() => onBack(() => (onCancel(), true)), [onCancel]);
  useEffect(() => {
    requestAnimationFrame(() => focusKey("pin-5"));
  }, []);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (/^[0-9]$/.test(e.key)) {
        e.preventDefault();
        setPin((p) => (p.length < 4 ? p + e.key : p));
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  useEffect(() => {
    if (pin.length < 4 || busy.current) return;
    busy.current = true;
    onEnter(pin).then((e) => {
      busy.current = false;
      if (e) {
        setErr(e);
        setPin("");
      }
    });
  }, [pin, onEnter]);
  const press = (k: string) => {
    if (k === "back") return setPin((p) => p.slice(0, -1));
    if (k === "cancel") return onCancel();
    setErr(undefined);
    setPin((p) => (p.length < 4 ? p + k : p));
  };
  return (
    <div className="tv-pf-pin" data-scope>
      <div className="tv-pf-pin-card">
        <h2 className="tv-modal-title">{title}</h2>
        {sub && <p className="tv-muted">{sub}</p>}
        <div className="tv-pf-dots">{[0, 1, 2, 3].map((i) => <span key={i} className={i < pin.length ? "on" : ""} />)}</div>
        <div className={`tv-error ${err ? "" : "hide"}`}>{err || " "}</div>
        <div className="tv-pf-keys">
          {[0, 1, 2, 3].map((r) => (
            <div key={r} className="tv-pf-keyrow" data-row>
              {PAD.slice(r * 3, r * 3 + 3).map((k) => (
                <button key={k} data-f data-fk={`pin-${k}`} className={`tv-key tv-pf-key ${k.length > 1 ? "fn" : ""}`}
                  aria-label={k} onClick={() => press(k)}>
                  {k === "back" ? "⌫" : k === "cancel" ? <X size={24} /> : k}
                </button>
              ))}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

/** Row of choices that remembers which one is picked. */
function Choices<T extends string>({ id, items, value, onPick, render, label }: {
  id: string; items: T[]; value: T; onPick: (v: T) => void; render: (v: T) => React.ReactNode; label: string;
}) {
  return (
    <div className="tv-pf-field" data-row>
      <div className="tv-pf-label">{label}</div>
      <div className="tv-pf-choices" data-group={`pf-${id}`} data-hscroll="reveal">
        {items.map((v) => (
          <button key={v} data-f data-fk={`${id}-${v}`} data-default={v === value ? "" : undefined}
            className={`tv-pf-choice ${v === value ? "on" : ""}`} aria-label={v} onClick={() => onPick(v)}>
            {render(v)}
          </button>
        ))}
      </div>
    </div>
  );
}

function Editor({ p, info, onDone }: { p?: Profile; info: ProfilesInfo; onDone: () => void }) {
  const [name, setName] = useState(p?.name || "");
  const [avatar, setAvatar] = useState(p?.avatar ?? AVATARS[info.profiles.length % AVATARS.length]);
  const [color, setColor] = useState(p?.color || Object.keys(COLORS)[(info.profiles.length * 3) % 8]);
  const [kids, setKids] = useState(p?.kids || false);
  const [maxAge, setMaxAge] = useState(p?.maxAge ?? 8);
  // undefined: unchanged; "": remove; "1234": new PIN.
  const [pin, setPin] = useState<string>();
  const [pinPad, setPinPad] = useState(false);
  const [err, setErr] = useState<string>();
  const [confirmDelete, setConfirmDelete] = useState(false);
  useEffect(() => onBack(() => (onDone(), true)), [onDone]);
  useEffect(() => {
    requestAnimationFrame(() => focusInitial(p ? "save" : "name"));
  }, []);
  const hasPin = pin === undefined ? !!p?.hasPin : pin !== "";

  const save = async () => {
    if (!name.trim()) {
      setErr("Give the profile a name.");
      return focusKey("name");
    }
    const body: ProfileInput = { name, avatar, color, pin };
    if (!p?.isAccount) Object.assign(body, { kids, maxAge });
    try {
      if (p) await updateProfile(p.id, body);
      else await createProfile(body);
      onDone();
    } catch (e) {
      setErr((e as Error).message);
    }
  };
  const remove = async () => {
    if (!p) return;
    if (!confirmDelete) return setConfirmDelete(true);
    await deleteProfile(p.id).then(onDone, (e: Error) => setErr(e.message));
  };

  return (
    <div className="tv-pf-editor" data-scope>
      <div className="tv-pf-editor-panel" data-vscroll="reveal" data-margin="4rem">
        <div className="tv-pf-editor-head" data-row>
          <TvAvatar p={{ id: p?.id ?? 0, name, avatar, color }} className="big" />
          <div className="tv-pf-editor-name">
            <h2 className="tv-modal-title">{p ? `Edit ${p.name}` : "New profile"}</h2>
            <input data-f data-fk="name" className="tv-pf-input" placeholder="Name" maxLength={24} value={name}
              onChange={(e) => setName(e.target.value)} />
          </div>
        </div>
        <Choices id="avatar" label="Picture" items={["", ...AVATARS]} value={avatar} onPick={setAvatar}
          render={(a) => (a ? <span className="e">{a}</span> : <b>{initialOf(name)}</b>)} />
        <Choices id="color" label="Colour" items={Object.keys(COLORS)} value={color} onPick={setColor}
          render={(c) => <span className="tv-pf-swatch" style={{ background: COLORS[c] }} />} />
        {!p?.isAccount && (
          <div className="tv-pf-field" data-row>
            <div className="tv-pf-label">Kids profile</div>
            <div className="tv-pf-choices" data-group="pf-kids">
              <button data-f data-fk="kids-off" data-default={!kids ? "" : undefined} className={`tv-btn chip ${!kids ? "on" : ""}`} onClick={() => setKids(false)}>
                <span>Off</span>
              </button>
              {info.levels.map((l) => (
                <button key={l.maxAge} data-f data-fk={`kids-${l.maxAge}`} data-default={kids && maxAge === l.maxAge ? "" : undefined}
                  className={`tv-btn chip tv-pf-level ${kids && maxAge === l.maxAge ? "on" : ""}`}
                  onClick={() => (setKids(true), setMaxAge(l.maxAge))}>
                  <span>{l.label}</span><span className="d">{l.detail}</span>
                </button>
              ))}
            </div>
          </div>
        )}
        <div className="tv-pf-field" data-row>
          <div className="tv-pf-label">PIN {hasPin ? "· on" : "· off"}</div>
          <div className="tv-pf-choices" data-group="pf-pin">
            <TvButton fk="pin-set" className="chip" icon={<Lock size={18} />} onClick={() => setPinPad(true)}>{hasPin ? "Change PIN" : "Set a PIN"}</TvButton>
            {hasPin && <TvButton fk="pin-remove" className="chip" onClick={() => setPin("")}>Remove PIN</TvButton>}
          </div>
        </div>
        <p className="tv-muted tv-pf-note">
          {p?.isAccount
            ? "A PIN on your main profile stops other profiles, like the kids, switching to it."
            : kids
              ? "Only titles rated for this age show up. Titles without a rating are hidden."
              : "Anyone switching to this profile needs the PIN, except from your main profile."}
        </p>
        {err && <div className="tv-error">{err}</div>}
        <div className="tv-actions tv-pf-editor-actions" data-row>
          <TvButton primary fk="save" icon={<Check size={22} />} onClick={save}>{p ? "Save" : "Add profile"}</TvButton>
          <TvButton fk="cancel" onClick={onDone}>Cancel</TvButton>
          {p && !p.isAccount && (
            <TvButton fk="delete" icon={<Trash size={20} />} onClick={remove}>{confirmDelete ? "Press again to delete" : "Delete"}</TvButton>
          )}
        </div>
      </div>
      {pinPad && (
        <PinPad title="Choose a 4-digit PIN" onCancel={() => (setPinPad(false), requestAnimationFrame(() => focusKey("pin-set")))}
          onEnter={async (v) => {
            setPin(v);
            setPinPad(false);
            requestAnimationFrame(() => focusKey("save"));
          }} />
      )}
    </div>
  );
}

/** Full-screen "Who's watching?" with management. */
export function TvWhoIsWatching({ onPicked, onClose, manage: startManage = false }: {
  onPicked: (u: User) => void; onClose?: () => void; manage?: boolean;
}) {
  const [info, setInfo] = useState<ProfilesInfo>();
  const [err, setErr] = useState<string>();
  const [manage, setManage] = useState(startManage);
  const [pinFor, setPinFor] = useState<Profile>();
  const [editing, setEditing] = useState<Profile | "new">();
  const lastFk = useRef<string>();
  const reload = () => loadProfiles().then(setInfo, (e: Error) => setErr(e.message));
  useEffect(() => {
    reload();
  }, []);
  useEffect(() => {
    if (!info || editing || pinFor) return;
    requestAnimationFrame(() => focusInitial(lastFk.current || `pf-${info.current}`));
  }, [info, editing, pinFor]);
  useEffect(
    () =>
      onBack(() => {
        if (manage && !startManage) return setManage(false), true;
        if (onClose) return onClose(), true;
        return false;
      }),
    [manage, onClose, startManage],
  );

  const pick = (p: Profile) => {
    if (!info) return;
    lastFk.current = `pf-${p.id}`;
    if (manage) return setEditing(p);
    if (needsPin(p, info)) return setPinFor(p);
    switchProfile(p.id).then(onPicked, (e: Error) => setErr(e.message));
  };

  return (
    <div className="tv-app tv-pf-screen">
      <div className="tv-grain" />
      <h1 className="tv-h1">{manage ? "Manage profiles" : "Who's watching?"}</h1>
      {err && <div className="tv-error">{err}</div>}
      {!info && !err && <Spinner />}
      {info && (
        <div className="tv-pf-grid" data-row data-group="pf-grid">
          {info.profiles.map((p) => (
            <button key={p.id} data-f data-fk={`pf-${p.id}`} className="tv-pf-tile" onClick={() => pick(p)}
              style={{ "--pf": colorOf(p) } as React.CSSProperties}>
              <span className="tv-pf-tile-art">
                <TvAvatar p={p} />
                {manage && <span className="tv-pf-edit"><Pencil size={36} /></span>}
              </span>
              <span className="tv-pf-name">{p.name}</span>
              <span className="tv-pf-tags">
                {p.kids && <span className="tv-pf-kids">KIDS</span>}
                {p.hasPin && <Lock size={18} />}
              </span>
            </button>
          ))}
          {info.canManage && manage && info.profiles.length <= info.max && (
            <button data-f data-fk="pf-add" className="tv-pf-tile" onClick={() => ((lastFk.current = "pf-add"), setEditing("new"))}>
              <span className="tv-pf-tile-art"><span className="tv-pf-avatar add"><Plus size={56} /></span></span>
              <span className="tv-pf-name">Add profile</span>
              <span className="tv-pf-tags" />
            </button>
          )}
        </div>
      )}
      {info?.canManage && (
        <div className="tv-actions tv-pf-actions" data-row>
          <TvButton fk="pf-manage" onClick={() => setManage(!manage)}>{manage ? "Done" : "Manage profiles"}</TvButton>
        </div>
      )}
      {pinFor && info && (
        <PinPad title={`Enter ${pinFor.name}'s PIN`}
          onCancel={() => setPinFor(undefined)}
          onEnter={(pin) => switchProfile(pinFor.id, pin).then(onPicked, (e: Error) => e.message)} />
      )}
      {editing && info && (
        <Editor p={editing === "new" ? undefined : editing} info={info} onDone={() => (setEditing(undefined), reload())} />
      )}
    </div>
  );
}
