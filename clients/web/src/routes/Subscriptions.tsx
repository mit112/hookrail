import { useState, useEffect } from "react";
import { Link } from "react-router-dom";
import { useSubscriptions } from "../query/subscriptions";
import type { TSubscriptionRow } from "../api/schemas";
import { RequireRole } from "../auth/role";

export function Subscriptions() {
  const [cursors, setCursors] = useState<string[]>([""]);
  const [allItems, setAllItems] = useState<TSubscriptionRow[]>([]);
  const currentCursor = cursors[cursors.length - 1];

  const { data, isLoading, isError, error } = useSubscriptions(undefined, currentCursor || undefined);

  // Accumulate items from each page
  useEffect(() => {
    if (data) {
      const items = data.items ?? [];
      setAllItems((prev) => {
        if (prev.length === 0) return items;
        const existingIds = new Set(prev.map((i) => i.id));
        const newItems = items.filter((i) => !existingIds.has(i.id));
        if (newItems.length === 0) return prev;
        return [...prev, ...newItems];
      });
    }
  }, [data]);

  if (isLoading && allItems.length === 0) return <p>Loading…</p>;
  if (isError) return <p role="alert">{String(error)}</p>;

  const nextCursor = data?.next_cursor ?? "";

  return (
    <div>
      <h1>Subscriptions</h1>
      <RequireRole min="admin"><Link to="/subscriptions/new">New Subscription</Link></RequireRole>
      <table>
        <thead>
          <tr>
            <th>ID</th>
            <th>Topic Pattern</th>
            <th>Endpoint</th>
            <th>Max Attempts</th>
            <th>Active</th>
          </tr>
        </thead>
        <tbody>
          {allItems.map((sub) => (
            <tr key={sub.id}>
              <td data-label="ID"><Link to={`/subscriptions/${sub.id}`}>{sub.id}</Link></td>
              <td data-label="Topic Pattern">{sub.topic_pattern}</td>
              <td data-label="Endpoint">{sub.endpoint_id}</td>
              <td data-label="Max Attempts">{sub.max_attempts}</td>
              <td data-label="Active">{String(sub.active)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {nextCursor && (
        <button onClick={() => setCursors((prev) => [...prev, nextCursor])}>
          Load more
        </button>
      )}
    </div>
  );
}
