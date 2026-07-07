import { useState, useEffect } from "react";
import { Link } from "react-router-dom";
import { useDeliveries, type DeliveryFilters } from "../query/deliveries";
import { useEndpoints } from "../query/endpoints";
import { DeliveryState, type TDeliveryListRow } from "../api/schemas";
import { serviceForEndpoint } from "../lib/endpointName";
import { StatePill } from "../components/StatePill";

function shortId(id: string): string {
  return id.length > 18 ? `${id.slice(0, 12)}...` : id;
}

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
  const endpoints = useEndpoints();
  const endpointsById = new Map((endpoints.data?.items ?? []).map((e) => [e.id, e]));

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
      <p className="page-lede">
        Every delivery attempt Hookrail has made, newest first. Filter by state
        to isolate what succeeded, what is still retrying, or what dead-lettered.
      </p>
      <div className="filters">
        <label className="filter-field">
          <span>State</span>
          <select value={inputState} onChange={(e) => setInputState(e.target.value)}>
            <option value="">Any state</option>
            {DeliveryState.options.map((s) => (
              <option key={s} value={s}>{s.replace(/_/g, " ")}</option>
            ))}
          </select>
        </label>
        <label className="filter-field">
          <span>Endpoint ID</span>
          <input
            placeholder="01J…"
            value={inputEndpointId}
            onChange={(e) => setInputEndpointId(e.target.value)}
          />
        </label>
        <label className="filter-field">
          <span>Topic</span>
          <input
            placeholder="orders.created"
            value={inputTopic}
            onChange={(e) => setInputTopic(e.target.value)}
          />
        </label>
        <label className="filter-field">
          <span>Event ID</span>
          <input
            placeholder="01J…"
            value={inputEventId}
            onChange={(e) => setInputEventId(e.target.value)}
          />
        </label>
        <button onClick={applyFilters}>Filter</button>
      </div>
      <table className="data-cards">
        <thead>
          <tr>
            <th>Delivery</th>
            <th>Endpoint</th>
            <th>Event</th>
            <th>State</th>
          </tr>
        </thead>
        <tbody>
          {allItems.map((d) => (
            <tr key={d.id}>
              <td data-label="Delivery"><Link to={`/deliveries/${d.id}`}>{shortId(d.id)}</Link></td>
              <td className="cell-service" data-label="Endpoint" title={d.endpoint_id}>
                {serviceForEndpoint(endpointsById.get(d.endpoint_id), shortId(d.endpoint_id))}
              </td>
              <td className="cell-mono" data-label="Event">{shortId(d.event_id)}</td>
              <td data-label="State"><StatePill state={d.state} /></td>
            </tr>
          ))}
        </tbody>
      </table>
      {nextCursor && (
        <button className="btn--ghost" onClick={() => setCursors((prev) => [...prev, nextCursor])}>
          Load more
        </button>
      )}
    </div>
  );
}
