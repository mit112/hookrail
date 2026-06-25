import { useState, type FormEvent } from "react";
import { ApiProblem } from "../api/client";
import { login } from "./session";

export function Login({ onSuccess }: { onSuccess: () => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    setPending(true);
    try {
      await login(username, password);
      onSuccess();
    } catch (err) {
      setError(err instanceof ApiProblem ? err.detail ?? err.title : "Login failed");
    } finally {
      setPending(false);
    }
  };

  return (
    <div className="auth">
      <div className="auth-card">
        <div className="auth-brand">
          <span className="brand-mark" aria-hidden="true" />
          <span className="brand-name">Hookrail</span>
        </div>
        <h2>Sign in</h2>
        <p className="auth-sub">Webhook delivery console</p>
        <form className="auth-form" onSubmit={submit}>
          <label htmlFor="username">Username</label>
          <input
            id="username"
            type="text"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            aria-label="Username"
            autoComplete="username"
          />
          <label htmlFor="password">Password</label>
          <input
            id="password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            aria-label="Password"
            autoComplete="current-password"
          />
          {error && <p role="alert">{error}</p>}
          <button type="submit" disabled={pending}>
            {pending ? "Signing in…" : "Sign in"}
          </button>
        </form>
      </div>
    </div>
  );
}
