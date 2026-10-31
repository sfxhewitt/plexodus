import { FormEvent, useEffect, useState } from "react";
import { api, User } from "../api";
import { useUser } from "../hooks";
import { native } from "../native";

export default function Login({ setup, invite }: { setup: boolean; invite?: string }) {
  const { setUser } = useUser();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [inviter, setInviter] = useState<string>();

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
        <input autoFocus placeholder="Username" value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" />
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
