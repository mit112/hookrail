import { useParams, Link } from "react-router-dom";
import { useSubscription } from "../query/subscriptions";
import { RequireRole } from "../auth/role";

export function SubscriptionDetail() {
  const { id } = useParams<{ id: string }>();
  const { data, isLoading, isError, error } = useSubscription(id!);

  if (isLoading) return <p>Loading…</p>;
  if (isError) return <p role="alert">{String(error)}</p>;
  if (!data) return null;

  return (
    <div>
      <h1>Subscription {data.id}</h1>
      <dl>
        <dt>Topic Pattern</dt>
        <dd>{data.topic_pattern}</dd>
        <dt>Endpoint ID</dt>
        <dd>{data.endpoint_id}</dd>
        <dt>Max Attempts</dt>
        <dd>{data.max_attempts}</dd>
        <dt>Rate Limit (rps)</dt>
        <dd>{data.rate_limit_rps ?? "—"}</dd>
        <dt>Active</dt>
        <dd>{String(data.active)}</dd>
        {data.backoff_policy != null && (
          <>
            <dt>Backoff Policy</dt>
            <dd><pre>{JSON.stringify(data.backoff_policy, null, 2)}</pre></dd>
          </>
        )}
        {data.deleted_at && (
          <>
            <dt>Deleted</dt>
            <dd>{data.deleted_at}</dd>
          </>
        )}
      </dl>
      <RequireRole min="admin"><Link to={`/subscriptions/${data.id}/edit`}>Edit</Link></RequireRole>
      {" | "}
      <Link to="/subscriptions">← Back to subscriptions</Link>
    </div>
  );
}
