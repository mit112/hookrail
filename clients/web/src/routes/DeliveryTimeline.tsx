import { useParams, Link } from "react-router-dom";
import { useDeliveryTimeline } from "../query/deliveries";

export function DeliveryTimeline() {
  const { id } = useParams<{ id: string }>();
  const { data, isLoading, isError, error } = useDeliveryTimeline(id!);

  if (isLoading) return <p>Loading…</p>;
  if (isError) return <p role="alert">{String(error)}</p>;
  if (!data) return null;

  return (
    <div>
      <h1>Delivery {data.delivery_id}</h1>
      <p>State: {data.state}</p>
      {data.attempts_truncated && <p role="alert">Attempts truncated</p>}
      <table>
        <thead>
          <tr>
            <th>Attempt #</th>
            <th>Claim Ver</th>
            <th>Status</th>
            <th>HTTP Status</th>
            <th>Error Class</th>
            <th>Latency (ms)</th>
          </tr>
        </thead>
        <tbody>
          {data.attempts.map((a, i) => (
            <tr key={i}>
              <td>{a.attempt_no}</td>
              <td>{a.claim_version}</td>
              <td>{a.status}</td>
              <td>{a.http_status}</td>
              <td>{a.error_class}</td>
              <td>{a.latency_ms}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <p><Link to="/deliveries">← Back to deliveries</Link></p>
    </div>
  );
}
