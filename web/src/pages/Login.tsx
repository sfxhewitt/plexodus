import { FormEvent, useState } from "react";
import { api, User } from "../api";
import { useUser } from "../hooks";

export default function Login({ setup }: { setup: boolean }) {
  const { setUser } = useUser();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      const u = await api.post<User>(setup ? "/api/setup" : "/api/login", { username, password });
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
        <p className="muted">{setup ? "Create the admin account to get started." : "Sign in to your library."}</p>
        <input autoFocus placeholder="Username" value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" />
        <input type="password" placeholder="Password" value={password} onChange={(e) => setPassword(e.target.value)}
          autoComplete={setup ? "new-password" : "current-password"} />
        {error && <div className="err">{error}</div>}
        <button className="btn primary wide" disabled={busy || !username || !password}>
          {busy ? <div className="spinner sm" /> : setup ? "Create account" : "Sign in"}
        </button>
      </form>
    </div>
  );
}
