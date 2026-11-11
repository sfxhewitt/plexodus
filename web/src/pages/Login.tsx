import { FormEvent, useEffect, useState } from "react";
import { api, User } from "../api";
import { useUser } from "../hooks";
import { native } from "../native";
import { PlexPin, startPlexPin, waitForPlex } from "../plexAuth";

/** "Sign in with Plex": a plex.tv window in a browser, a code for plex.tv/link in the app. */
function PlexSignIn({ onError }: { onError: (m?: string) => void }) {
  const { setUser } = useUser();
  const [pin, setPin] = useState<PlexPin>();
  const [busy, setBusy] = useState(false);
  const [signal] = useState({ cancelled: false });
  useEffect(() => () => { signal.cancelled = true; }, [signal]);

  const start = async () => {
    onError(undefined);
    setBusy(true);
    // Open the window now, while the click still counts, then point it at plex.tv.
    const popup = native ? null : window.open("", "plexodus-plex", "width=520,height=720");
    try {
      const p = await startPlexPin(!native);
      setPin(p);
      if (popup) popup.location.href = p.authUrl;
      const u = await waitForPlex(p, signal);
      popup?.close();
      setUser(u);
    } catch (e) {
      popup?.close();
      setPin(undefined);
      onError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  if (pin && native) {
    return (
      <div className="plex-code">
        <div className="muted small">On your phone or computer, go to</div>
        <a href="https://plex.tv/link" target="_blank" rel="noreferrer"><b>plex.tv/link</b></a>
        <div className="muted small">and enter</div>
        <div className="plex-code-value">{pin.code}</div>
        <div className="muted small"><div className="spinner sm" /> Waiting for Plex…</div>
      </div>
    );
  }
  return (
    <>
      <button type="button" className="btn plex wide" onClick={start} disabled={busy}>
        {busy ? <><div className="spinner sm" /> Waiting for Plex…</> : "Sign in with Plex"}
      </button>
      {busy && pin && !native && (
        <a className="muted small" href={pin.authUrl} target="_blank" rel="noreferrer">Plex window didn't open? Click here</a>
      )}
    </>
  );
}

export default function Login({ setup, invite }: { setup: boolean; invite?: string }) {
  const { setUser } = useUser();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [inviter, setInviter] = useState<string>();
  const [plex, setPlex] = useState(false);

  useEffect(() => {
    if (setup || invite) return;
    api.get<{ plexSignIn?: boolean }>("/api/status").then((s) => setPlex(!!s.plexSignIn), () => {});
  }, [setup, invite]);

  useEffect(() => {
    if (!invite) return;
    api.get<{ inviter: string }>(`/api/invites/${invite}`).then((d) => setInviter(d.inviter), (e) => setError(e.message));
  }, [invite]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      const url = invite ? `/api/invites/${invite}/accept` : setup ? "/api/setup" : "/api/login";
      const u = await api.post<User>(url, { username, password });
      if (invite) window.history.replaceState(null, "", "/");
      setUser(u);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="login">
      <div className="login-aurora" />
      <form className="login-card" onSubmit={submit}>
        <img src="/favicon.svg" alt="" width={56} height={56} />
        <h1>plex<b>odus</b></h1>
        <p className="muted">
          {invite
            ? inviter ? `${inviter} invited you. Choose a username and password.` : "Checking your invite…"
            : setup ? "Create the admin account to get started." : "Sign in to your library."}
        </p>
        {plex && (
          <>
            <PlexSignIn onError={setError} />
            <div className="or muted small"><span>or use a Plexodus password</span></div>
          </>
        )}
        <input autoFocus={!plex} placeholder="Username" value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" />
        <input type="password" placeholder="Password" value={password} onChange={(e) => setPassword(e.target.value)}
          autoComplete={setup || invite ? "new-password" : "current-password"} />
        {error && <div className="err">{error}</div>}
        <button className="btn primary wide" disabled={busy || !username || !password || (!!invite && !inviter)}>
          {busy ? <div className="spinner sm" /> : setup || invite ? "Create account" : "Sign in"}
        </button>
        {native && (
          <button type="button" className="link-btn muted small" onClick={() => native?.changeServer()}>
            Connected to {window.location.host} · Change server
          </button>
        )}
      </form>
    </div>
  );
}
