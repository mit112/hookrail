import { useState, useEffect } from "react";
import { Link } from "react-router-dom";
import { useDeliveries, type DeliveryFilters } from "../query/deliveries";
import type { TDeliveryListRow } from "../api/schemas";

export function Deliveries() {
  const [cursors, setCursors] = useState<string[]>([""]);
  const [allItems, setAllItems] = useState<TDeliveryListRow[]>([]);
  const [filters, setFilters] = useState<DeliveryFilters>({});
  const [inputState, setInputState] = useState("");
  const [inputEndpointId, setInputEndpointId] = useState("");
  const [inputTopic, setInputTopic] = useState("");
  const [inputEventId, setInputEventId] = useState("");
  const currentCursor = cursors[cursors.length - 1];

  const { data, isLoading, isError, error } = useDeliveries(
    Object.keys(filters).length > 0 ? filters : undefined,
    currentCursor || undefined,
  );

  // Reset pagination when filters change
  const applyFilters = () => {
    const f: DeliveryFilters = {};
    if (inputState.trim()) f.state = inputState.trim();
    if (inputEndpointId.trim()) f.endpoint_id = inputEndpointId.trim();
    if (inputTopic.trim()) f.topic = inputTopic.trim();
    if (inputEventId.trim()) f.event_id = inputEventId.trim();
    setCursors([""]);
    setAllItems([]);
    setFilters(f);
  };

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
      <h1>Deliveries</h1>
      <div>
        <input
          placeholder="state"
          value={inputState}
          onChange={(e) => setInputState(e.target.value)}
        />
        <input
          placeholder="endpoint_id"
          value={inputEndpointId}
          onChange={(e) => setInputEndpointId(e.target.value)}
        />
        <input
          placeholder="topic"
          value={inputTopic}
          onChange={(e) => setInputTopic(e.target.value)}
        />
        <input
          placeholder="event_id"
          value={inputEventId}
          onChange={(e) => setInputEventId(e.target.value)}
        />
        <button onClick={applyFilters}>Filter</button>
      </div>
      <table>
        <thead>
          <tr>
            <th>ID</th>
            <th>Event</th>
            <th>Endpoint</th>
            <th>State</th>
          </tr>
        </thead>
        <tbody>
          {allItems.map((d) => (
            <tr key={d.id}>
              <td><Link to={`/deliveries/${d.id}`}>{d.id}</Link></td>
              <td>{d.event_id}</td>
              <td>{d.endpoint_id}</td>
              <td>{d.state}</td>
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
