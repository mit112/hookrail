import { useState } from "react";
import { useTestEvent } from "../query/testEvent";
import { ApiProblem } from "../api/client";
import { RequireRole } from "../auth/role";

export function TestEvent() {
  const [topic, setTopic] = useState("");
  const [payloadText, setPayloadText] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [clientError, setClientError] = useState<string | null>(null);
  const [result, setResult] = useState<{ event_id: string; delivery_ids: string[] } | null>(null);

  const mutation = useTestEvent();

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setClientError(null);
    setError(null);
    setResult(null);

    // Validate topic
    if (!topic.trim()) {
      setClientError("Topic is required");
      return;
    }

    // Validate payload is valid JSON object
    let payload: unknown;
    try {
      payload = JSON.parse(payloadText);
    } catch {
      setClientError("Payload must be valid JSON");
      return;
    }

    try {
      const data = await mutation.mutateAsync({ topic: topic.trim(), payload });
      setResult(data as { event_id: string; delivery_ids: string[] });
    } catch (err) {
      if (err instanceof ApiProblem) {
        setError(err.detail ?? err.title);
      } else {
        setError(String(err));
      }
    }
  };

  return (
    <div>
      <h1>Send Test Event</h1>
      <p>Each send is a new event.</p>
      <form onSubmit={handleSubmit}>
        <div>
          <label htmlFor="topic">Topic</label>
          <input
            id="topic"
            type="text"
            value={topic}
            onChange={(e) => setTopic(e.target.value)}
            disabled={mutation.isPending}
          />
        </div>
        <div>
          <label htmlFor="payload">Payload (JSON)</label>
          <textarea
            id="payload"
            value={payloadText}
            onChange={(e) => setPayloadText(e.target.value)}
            disabled={mutation.isPending}
            rows={6}
          />
        </div>
        {clientError && <p role="alert">{clientError}</p>}
        {error && <p role="alert">{error}</p>}
        <RequireRole min="operator">
          <button type="submit" disabled={mutation.isPending}>
            Send
          </button>
        </RequireRole>
      </form>
      {result && (
        <div>
          <h2>Event Sent</h2>
          <p>Event ID: <code>{result.event_id}</code></p>
          {result.delivery_ids.length > 0 && (
            <p>Delivery IDs: {result.delivery_ids.join(", ")}</p>
          )}
        </div>
      )}
    </div>
  );
}
