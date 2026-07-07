import { useParams, Link } from "react-router-dom";
import { useDeliveryTimeline } from "../query/deliveries";
import { StatePill } from "../components/StatePill";

// Map an attempt's status to the shared pill palette. Attempt statuses are
// distinct from delivery states (StatePill's own map), so classify here:
// retries read as "active" (amber), a success as "ok" (green), a permanent
// failure as "bad" (red).
function attemptVariant(status: string): "ok" | "active" | "bad" | "neutral" {
  const s = status.toLowerCase();
  if (s.includes("success") || s.includes("delivered") || s.includes("ok")) return "ok";
  if (s.includes("retry")) return "active";
  if (s.includes("permanent") || s.includes("fatal") || s.includes("dead") || s.includes("gave")) return "bad";
  return "neutral";
}

export function DeliveryTimeline() {
  const { id } = useParams<{ id: string }>();
  const { data, isLoading, isError, error } = useDeliveryTimeline(id!);

  if (isLoading) return <p>Loading…</p>;
  if (isError) return <p role="alert">{String(error)}</p>;
  if (!data) return null;

  return (
    <div>
      <div className="detail-head">
        <h1>Delivery {data.delivery_id}</h1>
        <StatePill state={data.state} />
      </div>
      <p className="page-lede">
        Every attempt Hookrail made for this event, in order. It retries with
        exponential backoff until the attempt budget is spent — then the delivery
        moves to the dead-letter queue for review.
      </p>
      {data.attempts_truncated && <p role="alert">Attempts truncated</p>}
      <table>
        <thead>
          <tr>
            <th>Attempt #</th>
            <th>Claim ver</th>
            <th>Status</th>
            <th>HTTP</th>
            <th>Error class</th>
            <th>Latency (ms)</th>
          </tr>
        </thead>
        <tbody>
          {data.attempts.map((a, i) => (
            <tr key={i}>
              <td data-label="Attempt #">{a.attempt_no}</td>
              <td data-label="Claim ver">{a.claim_version}</td>
              <td data-label="Status">
                <span className={`pill pill--${attemptVariant(a.status)}`} title={a.status}>
                  <span className="pill-dot" aria-hidden="true" />
                  {a.status.replace(/_/g, " ")}
                </span>
              </td>
              <td data-label="HTTP">{a.http_status ?? "—"}</td>
              <td data-label="Error class">{a.error_class ?? "—"}</td>
              <td data-label="Latency (ms)">{a.latency_ms}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <p><Link to="/deliveries">← Back to deliveries</Link></p>
    </div>
  );
}
