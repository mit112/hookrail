import { useState, useEffect } from "react";
import { Link } from "react-router-dom";
import { useEndpoints } from "../query/endpoints";
import type { TEndpointRow } from "../api/schemas";
import { RequireRole } from "../auth/role";

// Compact an ISO timestamp to "YYYY-MM-DD HH:MM:SS"; leave non-ISO untouched.
function fmtWhen(s: string): string {
  if (!/^\d{4}-\d{2}-\d{2}T/.test(s)) return s;
  return s.slice(0, 19).replace("T", " ");
}

export function Endpoints() {
  const [cursors, setCursors] = useState<string[]>([""]);
  const [allItems, setAllItems] = useState<TEndpointRow[]>([]);
  const currentCursor = cursors[cursors.length - 1];

  const { data, isLoading, isError, error } = useEndpoints(currentCursor || undefined);

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
      <h1>Endpoints</h1>
      <p className="page-lede">
        The webhook receivers Hookrail delivers to. In this demo, three services —
        orders, payments, and analytics — each take a different reliability path
        (success, flaky, and hard-fail) to exercise the delivery pipeline.
      </p>
      <RequireRole min="admin"><Link to="/endpoints/new">+ New endpoint</Link></RequireRole>
      <table>
        <thead>
          <tr>
            <th>ID</th>
            <th>URL</th>
            <th>Description</th>
            <th>Created</th>
          </tr>
        </thead>
        <tbody>
          {allItems.map((ep) => (
            <tr key={ep.id}>
              <td data-label="ID"><Link to={`/endpoints/${ep.id}`}>{ep.id}</Link></td>
              <td data-label="URL">{ep.url}</td>
              <td data-label="Description">{ep.description}</td>
              <td data-label="Created">{fmtWhen(ep.created_at)}</td>
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
