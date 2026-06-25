// StatePill renders a webhook delivery state as a color-coded pill — the
// dashboard's signature element, since delivery state is the core data.
const VARIANT: Record<string, "ok" | "active" | "bad" | "neutral" | "skip"> = {
  succeeded: "ok",
  delivered: "ok",
  delivering: "active",
  pending: "active",
  retrying: "active",
  scheduled: "active",
  queued: "neutral",
  paused: "neutral",
  failed: "bad",
  dead_letter: "bad",
  "dead-letter": "bad",
  deadletter: "bad",
  skipped: "skip",
};

export function StatePill({ state }: { state: string }) {
  const key = state.trim().toLowerCase();
  const variant = VARIANT[key] ?? "neutral";
  const label = key.replace(/_/g, " ");
  return (
    <span className={`pill pill--${variant}`} title={state}>
      <span className="pill-dot" aria-hidden="true" />
      {label}
    </span>
  );
}
