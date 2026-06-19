import { SessionGate } from "./auth/SessionGate";

export function App() {
  return (
    <SessionGate>
      <h1>Hookrail</h1>
    </SessionGate>
  );
}
