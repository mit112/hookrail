import { useState, useEffect } from "react";
import { Link } from "react-router-dom";
import { useDLQ, type DLQFilters } from "../query/dlq";
import type { TDLQRow } from "../api/schemas";

export function DLQ() {
  const [cursors, setCursors] = useState<string[]>([""]);
  const [allItems, setAllItems] = useState<TDLQRow[]>([]);
  const [filters, setFilters] = useState<DLQFilters>({});
  const [inputEndpointId, setInputEndpointId] = useState("");
  const [inputReplayed, setInputReplayed] = useState("");
  const currentCursor = cursors[cursors.length - 1];

  const { data, isLoading, isError, error } = useDLQ(
    Object.keys(filters).length > 0 ? filters : undefined,
    currentCursor || undefined,
  );

  const applyFilters = () => {
    const f: DLQFilters = {};
    if (inputEndpointId.trim()) f.endpoint_id = inputEndpointId.trim();
    if (inputReplayed.trim()) f.replayed = inputReplayed.trim();
    setCursors([""]);
    setAllItems([]);
    setFilters(f);
  };

  useEffect(() => {
    if (data) {
      const items = data.items ?? [];
      setAllItems((prev) => {
        if (prev.length === 0) return items;
        const existingIds = new Set(prev.map((i) => i.delivery_id));
        const newItems = items.filter((i) => !existingIds.has(i.delivery_id));
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
      <h1>Dead Letter Queue</h1>
      <div>
        <input
          placeholder="endpoint_id"
          value={inputEndpointId}
          onChange={(e) => setInputEndpointId(e.target.value)}
        />
        <input
          placeholder="replayed"
          value={inputReplayed}
          onChange={(e) => setInputReplayed(e.target.value)}
        />
        <button onClick={applyFilters}>Filter</button>
      </div>
      <table>
        <thead>
          <tr>
            <th>Delivery</th>
            <th>Endpoint</th>
            <th>Final Error</th>
            <th>Dead At</th>
            <th>Replayed At</th>
          </tr>
        </thead>
        <tbody>
          {allItems.map((d) => (
            <tr key={d.delivery_id}>
              <td><Link to={`/deliveries/${d.delivery_id}`}>{d.delivery_id}</Link></td>
              <td>{d.endpoint_id}</td>
              <td>{d.final_error}</td>
              <td>{d.dead_at}</td>
              <td>{d.replayed_at || "—"}</td>
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
