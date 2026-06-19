import { useState } from "react";
import { useParams, Link } from "react-router-dom";
import { useEndpoint } from "../query/endpoints";
import { useRotateSecret } from "../query/rotateSecret";
import { SecretReveal } from "../components/SecretReveal";

export function EndpointDetail() {
  const { id } = useParams<{ id: string }>();
  const { data, isLoading, isError, error } = useEndpoint(id!);
  const rotateMutation = useRotateSecret();
  const [secret, setSecret] = useState<string | null>(null);
  const [rotateError, setRotateError] = useState<string | null>(null);

  if (isLoading) return <p>Loading…</p>;
  if (isError) return <p role="alert">{String(error)}</p>;
  if (!data) return null;

  const handleRotate = async () => {
    setRotateError(null);
    try {
      const result = await rotateMutation.mutateAsync(id!);
      setSecret(result.secret);
    } catch (err) {
      setRotateError(String(err));
    }
  };

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
      <button type="button" onClick={handleRotate} disabled={rotateMutation.isPending}>
        {rotateMutation.isPending ? "Rotating…" : "Rotate secret"}
      </button>
      {" | "}
      <Link to="/endpoints">← Back to endpoints</Link>
      {rotateError && <p role="alert">{rotateError}</p>}
      {secret && <SecretReveal secret={secret} onClose={() => setSecret(null)} />}
    </div>
  );
}
