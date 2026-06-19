import { useParams, Link } from "react-router-dom";
import { useEndpoint } from "../query/endpoints";

export function EndpointDetail() {
  const { id } = useParams<{ id: string }>();
  const { data, isLoading, isError, error } = useEndpoint(id!);

  if (isLoading) return <p>Loading…</p>;
  if (isError) return <p role="alert">{String(error)}</p>;
  if (!data) return null;

  return (
    <div>
      <h1>Endpoint {data.id}</h1>
      <dl>
        <dt>URL</dt>
        <dd>{data.url}</dd>
        <dt>Description</dt>
        <dd>{data.description}</dd>
        <dt>Created</dt>
        <dd>{data.created_at}</dd>
        {data.deleted_at && (
          <>
            <dt>Deleted</dt>
            <dd>{data.deleted_at}</dd>
          </>
        )}
      </dl>
      <Link to={`/endpoints/${data.id}/edit`}>Edit</Link>
      {" | "}
      <Link to="/endpoints">← Back to endpoints</Link>
    </div>
  );
}
