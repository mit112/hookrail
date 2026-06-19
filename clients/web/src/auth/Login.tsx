import { useState, type FormEvent } from "react";
import { ApiProblem } from "../api/client";
import { login } from "./session";

export function Login({ onSuccess }: { onSuccess: () => void }) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    setPending(true);
    try {
      await login(password);
      onSuccess();
    } catch (err) {
      setError(err instanceof ApiProblem ? err.detail ?? err.title : "Login failed");
    } finally {
      setPending(false);
    }
  };

  return (
    <form onSubmit={submit}>
      <label htmlFor="password">Password</label>
      <input
        id="password"
        type="password"
        value={password}
        onChange={(e) => setPassword(e.target.value)}
        aria-label="Password"
      />
      {error && <p role="alert">{error}</p>}
      <button type="submit" disabled={pending}>Sign in</button>
    </form>
  );
}
