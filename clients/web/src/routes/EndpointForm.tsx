import { useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { useCreateEndpoint, useUpdateEndpoint, useDeleteEndpoint } from "../query/endpointMutations";
import { useEndpoint } from "../query/endpoints";
import { ApiProblem } from "../api/client";
import type { TEndpointRow } from "../api/schemas";

interface EndpointFormProps {
  endpoint?: TEndpointRow;
  onSecret?: (secret: string) => void;
  onSuccess?: () => void;
}

export function EndpointForm({ endpoint, onSecret, onSuccess }: EndpointFormProps) {
  const isEdit = !!endpoint;
  const [url, setUrl] = useState(endpoint?.url ?? "");
  const [description, setDescription] = useState(endpoint?.description ?? "");
  const [error, setError] = useState<string | null>(null);
  const [clientError, setClientError] = useState<string | null>(null);
  const [showConfirm, setShowConfirm] = useState(false);

  const createMutation = useCreateEndpoint();
  const updateMutation = useUpdateEndpoint(endpoint?.id ?? "");
  const deleteMutation = useDeleteEndpoint();

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setClientError(null);
    setError(null);

    if (!url.trim()) {
      setClientError("URL is required");
      return;
    }

    try {
      if (isEdit) {
        const body: Record<string, unknown> = {};
        if (url !== endpoint!.url) body.url = url;
        if (description !== endpoint!.description) body.description = description;
        await updateMutation.mutateAsync(body);
        onSuccess?.();
      } else {
        const result = await createMutation.mutateAsync({ url: url.trim(), description: description.trim() || undefined });
        if (onSecret && result.secret) {
          onSecret(result.secret);
        }
      }
    } catch (err) {
      if (err instanceof ApiProblem) {
        setError(err.detail ?? err.title);
      } else {
        setError(String(err));
      }
    }
  };

  const handleDelete = async () => {
    if (!endpoint) return;
    setShowConfirm(false);
    setError(null);
    try {
      await deleteMutation.mutateAsync(endpoint.id);
      onSuccess?.();
    } catch (err) {
      if (err instanceof ApiProblem) {
        setError(err.detail ?? err.title);
      } else {
        setError(String(err));
      }
    }
  };

  const isPending = createMutation.isPending || updateMutation.isPending || deleteMutation.isPending;

  return (
    <div>
      <h2>{isEdit ? "Edit Endpoint" : "New Endpoint"}</h2>
      <form onSubmit={handleSubmit}>
        <div>
          <label htmlFor="url">URL</label>
          <input
            id="url"
            type="text"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            disabled={isPending}
          />
        </div>
        <div>
          <label htmlFor="description">Description</label>
          <input
            id="description"
            type="text"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            disabled={isPending}
          />
        </div>
        {clientError && <p role="alert">{clientError}</p>}
        {error && <p role="alert">{error}</p>}
        <button type="submit" disabled={isPending}>
          {isEdit ? "Save" : "Create"}
        </button>
        {isEdit && (
          <button
            type="button"
            onClick={() => setShowConfirm(true)}
            disabled={isPending}
          >
            Delete
          </button>
        )}
      </form>
      {showConfirm && (
        <div role="dialog">
          <p>Are you sure you want to delete this endpoint?</p>
          <button onClick={handleDelete}>Confirm</button>
          <button onClick={() => setShowConfirm(false)}>Cancel</button>
        </div>
      )}
    </div>
  );
}

export function EndpointNew() {
  const navigate = useNavigate();
  return (
    <EndpointForm onSecret={() => {}} onSuccess={() => navigate("/endpoints")} />
  );
}

export function EndpointEdit() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { data, isLoading, isError, error } = useEndpoint(id!);

  if (isLoading) return <p>Loading…</p>;
  if (isError) return <p role="alert">{String(error)}</p>;
  if (!data) return null;

  return <EndpointForm endpoint={data} onSuccess={() => navigate("/endpoints")} />;
}
