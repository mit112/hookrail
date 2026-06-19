import { useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { useCreateSubscription, useUpdateSubscription, useDeleteSubscription, CreateInput } from "../query/subscriptionMutations";
import { useSubscription } from "../query/subscriptions";
import { ApiProblem } from "../api/client";
import type { TSubscriptionRow } from "../api/schemas";

interface SubscriptionFormProps {
  subscription?: TSubscriptionRow;
  onSuccess?: () => void;
}

export function SubscriptionForm({ subscription, onSuccess }: SubscriptionFormProps) {
  const isEdit = !!subscription;
  const [topicPattern, setTopicPattern] = useState(subscription?.topic_pattern ?? "");
  const [endpointId, setEndpointId] = useState(subscription?.endpoint_id ?? "");
  const [maxAttempts, setMaxAttempts] = useState(subscription?.max_attempts ?? 3);
  const [rateLimitRps, setRateLimitRps] = useState(subscription?.rate_limit_rps);
  const [active, setActive] = useState(subscription?.active ?? true);
  const [error, setError] = useState<string | null>(null);
  const [clientError, setClientError] = useState<string | null>(null);
  const [showConfirm, setShowConfirm] = useState(false);

  const createMutation = useCreateSubscription();
  const updateMutation = useUpdateSubscription(subscription?.id ?? "");
  const deleteMutation = useDeleteSubscription();

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setClientError(null);
    setError(null);

    if (isEdit) {
      const body: Record<string, unknown> = {};
      if (maxAttempts !== subscription!.max_attempts) body.max_attempts = maxAttempts;
      if (rateLimitRps !== subscription!.rate_limit_rps) body.rate_limit_rps = rateLimitRps;
      if (active !== subscription!.active) body.active = active;

      try {
        await updateMutation.mutateAsync(body);
        onSuccess?.();
      } catch (err) {
        if (err instanceof ApiProblem) {
          setError(err.detail ?? err.title);
        } else {
          setError(String(err));
        }
      }
      return;
    }

    // Create mode: validate with zod
    const parsed = CreateInput.safeParse({
      topic_pattern: topicPattern.trim(),
      endpoint_id: endpointId.trim(),
      max_attempts: maxAttempts,
      rate_limit_rps: rateLimitRps,
    });

    if (!parsed.success) {
      setClientError(parsed.error.issues[0]?.message ?? "Validation error");
      return;
    }

    try {
      await createMutation.mutateAsync(parsed.data);
      onSuccess?.();
    } catch (err) {
      if (err instanceof ApiProblem) {
        if (err.status === 409) {
          setError("endpoint not available / subscription deleted");
        } else {
          setError(err.detail ?? err.title);
        }
      } else {
        setError(String(err));
      }
    }
  };

  const handleDelete = async () => {
    if (!subscription) return;
    setShowConfirm(false);
    setError(null);
    try {
      await deleteMutation.mutateAsync(subscription.id);
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
      <h2>{isEdit ? "Edit Subscription" : "New Subscription"}</h2>
      <form onSubmit={handleSubmit}>
        {isEdit ? (
          <>
            <div>
              <label>Topic Pattern</label>
              <p>{subscription!.topic_pattern}</p>
            </div>
            <div>
              <label>Endpoint ID</label>
              <p>{subscription!.endpoint_id}</p>
            </div>
          </>
        ) : (
          <>
            <div>
              <label htmlFor="topic_pattern">Topic Pattern</label>
              <input
                id="topic_pattern"
                type="text"
                value={topicPattern}
                onChange={(e) => setTopicPattern(e.target.value)}
                disabled={isPending}
              />
            </div>
            <div>
              <label htmlFor="endpoint_id">Endpoint ID</label>
              <input
                id="endpoint_id"
                type="text"
                value={endpointId}
                onChange={(e) => setEndpointId(e.target.value)}
                disabled={isPending}
              />
            </div>
          </>
        )}
        <div>
          <label htmlFor="max_attempts">Max Attempts</label>
          <input
            id="max_attempts"
            type="number"
            value={maxAttempts}
            onChange={(e) => setMaxAttempts(Number(e.target.value))}
            disabled={isPending}
          />
        </div>
        <div>
          <label htmlFor="rate_limit_rps">Rate Limit (rps)</label>
          <input
            id="rate_limit_rps"
            type="number"
            value={rateLimitRps ?? ""}
            onChange={(e) => setRateLimitRps(e.target.value ? Number(e.target.value) : undefined)}
            disabled={isPending}
          />
        </div>
        {isEdit && (
          <div>
            <label htmlFor="active">Active</label>
            <input
              id="active"
              type="checkbox"
              checked={active}
              onChange={(e) => setActive(e.target.checked)}
              disabled={isPending}
            />
          </div>
        )}
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
          <p>Are you sure you want to delete this subscription?</p>
          <button onClick={handleDelete}>Confirm</button>
          <button onClick={() => setShowConfirm(false)}>Cancel</button>
        </div>
      )}
    </div>
  );
}

export function SubscriptionNew() {
  const navigate = useNavigate();
  return <SubscriptionForm onSuccess={() => navigate("/subscriptions")} />;
}

export function SubscriptionEdit() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { data, isLoading, isError, error } = useSubscription(id!);
  if (isLoading) return <p>Loading…</p>;
  if (isError) return <p role="alert">{String(error)}</p>;
  if (!data) return null;
  return <SubscriptionForm subscription={data} onSuccess={() => navigate("/subscriptions")} />;
}
