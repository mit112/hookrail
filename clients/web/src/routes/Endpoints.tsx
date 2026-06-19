import { useState, useEffect } from "react";
import { Link } from "react-router-dom";
import { useEndpoints } from "../query/endpoints";
import type { TEndpointRow } from "../api/schemas";

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
      <Link to="/endpoints/new">+ New endpoint</Link>
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
              <td><Link to={`/endpoints/${ep.id}`}>{ep.id}</Link></td>
              <td>{ep.url}</td>
              <td>{ep.description}</td>
              <td>{ep.created_at}</td>
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
