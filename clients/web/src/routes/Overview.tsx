import { Link } from "react-router-dom";
import { StatePill } from "../components/StatePill";
import type { TDeliveryListRow, TDLQRow, TEndpointRow } from "../api/schemas";
import { useDeliveries } from "../query/deliveries";
import { useDLQ } from "../query/dlq";
import { useEndpoints } from "../query/endpoints";

type Lane = {
  key: "orders" | "payments" | "analytics";
  title: string;
  service: string;
  state: TDeliveryListRow["state"];
  match: string[];
  path: string;
  copy: string;
};

const LANES: Lane[] = [
  {
    key: "orders",
    title: "Delivering",
    service: "orders-service",
    state: "succeeded",
    match: ["orders-service", "/succeed", "success path", "demo.orders"],
    path: "success path",
    copy: "Successful events land after the receiver accepts the webhook.",
  },
  {
    key: "payments",
    title: "Retrying",
    service: "payments-service",
    state: "retry_scheduled",
    match: ["payments-service", "/flap", "flaky path", "demo.flaky"],
    path: "flaky path",
    copy: "Temporary failures stay visible while the next retry is scheduled.",
  },
  {
    key: "analytics",
    title: "Dead-lettered",
    service: "analytics-service",
    state: "dead_lettered",
    match: ["analytics-service", "/fail", "hard-fail path", "demo.retry"],
    path: "hard-fail path",
    copy: "Permanent failures stop retrying and move to dead letter for review.",
  },
];

function endpointText(endpoint: TEndpointRow): string {
  return `${endpoint.id} ${endpoint.url} ${endpoint.description}`.toLowerCase();
}

function endpointForLane(endpoints: TEndpointRow[], lane: Lane): TEndpointRow | undefined {
  return endpoints.find((endpoint) =>
    lane.match.some((needle) => endpointText(endpoint).includes(needle)),
  );
}

function serviceForEndpoint(endpoint?: TEndpointRow, fallbackId = ""): string {
  if (!endpoint) return fallbackId;
  const text = endpointText(endpoint);
  const lane = LANES.find((candidate) =>
    candidate.match.some((needle) => text.includes(needle)),
  );
  if (lane) return lane.service;

  try {
    const host = new URL(endpoint.url).hostname;
    if (host) return host;
  } catch {
    // Keep the dashboard readable even if a stored endpoint URL is malformed.
  }
  return endpoint.description.trim() || endpoint.id;
}

function deliveryForLane(
  deliveries: TDeliveryListRow[],
  lane: Lane,
  endpoint?: TEndpointRow,
): TDeliveryListRow | undefined {
  const endpointDeliveries = endpoint
    ? deliveries.filter((delivery) => delivery.endpoint_id === endpoint.id)
    : [];
  return (
    endpointDeliveries.find((delivery) => delivery.state === lane.state) ??
    endpointDeliveries[0] ??
    deliveries.find((delivery) => delivery.state === lane.state)
  );
}

function dlqForLane(
  rows: TDLQRow[],
  delivery?: TDeliveryListRow,
  endpoint?: TEndpointRow,
): TDLQRow | undefined {
  return rows.find((row) =>
    (delivery && row.delivery_id === delivery.id) ||
    (endpoint && row.endpoint_id === endpoint.id),
  );
}

function shortId(id: string): string {
  return id.length > 18 ? `${id.slice(0, 12)}...` : id;
}

export function Overview() {
  const endpoints = useEndpoints();
  const deliveries = useDeliveries();
  const dlq = useDLQ();

  if (
    (endpoints.isLoading && !endpoints.data) ||
    (deliveries.isLoading && !deliveries.data) ||
    (dlq.isLoading && !dlq.data)
  ) {
    return <p>Loading...</p>;
  }
  if (endpoints.isError || deliveries.isError || dlq.isError) {
    return <p role="alert">Unable to load the demo overview.</p>;
  }

  const endpointItems = endpoints.data?.items ?? [];
  const deliveryItems = deliveries.data?.items ?? [];
  const dlqItems = dlq.data?.items ?? [];
  const endpointsById = new Map(endpointItems.map((endpoint) => [endpoint.id, endpoint]));
  const recentDeliveries = deliveryItems.slice(0, 6);

  return (
    <div className="overview">
      <header className="overview-header">
        <div>
          <p className="overview-eyebrow">Public demo</p>
          <h1>Delivery overview</h1>
          <p className="overview-lede">
            Hookrail is delivering demo webhooks live. Successful events land,
            temporary failures retry, permanent failures go to dead letter.
          </p>
        </div>
        <span className="overview-live" aria-label="Live demo traffic">
          <span aria-hidden="true" />
          Live traffic
        </span>
      </header>

      <section className="overview-panels" aria-label="Delivery outcomes">
        {LANES.map((lane) => {
          const endpoint = endpointForLane(endpointItems, lane);
          const delivery = deliveryForLane(deliveryItems, lane, endpoint);
          const deadLetter = dlqForLane(dlqItems, delivery, endpoint);
          const finalError = lane.key === "analytics"
            ? deadLetter?.final_error ?? "http_500"
            : undefined;

          return (
            <article className={`overview-panel overview-panel--${lane.key}`} key={lane.key}>
              <div className="overview-panel__top">
                <span className="overview-panel__title">{lane.title}</span>
                <StatePill state={delivery?.state ?? lane.state} />
              </div>
              <h2>{endpoint ? serviceForEndpoint(endpoint) : lane.service}</h2>
              <p>{lane.copy}</p>
              <dl>
                <div>
                  <dt>Route</dt>
                  <dd>{lane.path}</dd>
                </div>
                <div>
                  <dt>Latest</dt>
                  <dd>{delivery ? shortId(delivery.event_id) : "waiting"}</dd>
                </div>
                {finalError && (
                  <div>
                    <dt>Final error</dt>
                    <dd>{finalError}</dd>
                  </div>
                )}
              </dl>
            </article>
          );
        })}
      </section>

      <section className="overview-recent" aria-labelledby="overview-recent-title">
        <div className="overview-section-head">
          <h2 id="overview-recent-title">Recent events</h2>
          <Link to="/deliveries">View deliveries</Link>
        </div>
        <table className="overview-table">
          <thead>
            <tr>
              <th>Endpoint</th>
              <th>Event</th>
              <th>State</th>
              <th>Delivery</th>
            </tr>
          </thead>
          <tbody>
            {recentDeliveries.map((delivery) => {
              const endpoint = endpointsById.get(delivery.endpoint_id);
              return (
                <tr key={delivery.id}>
                  <td>
                    <span className="overview-service">
                      {serviceForEndpoint(endpoint, delivery.endpoint_id)}
                    </span>
                  </td>
                  <td className="overview-id">{shortId(delivery.event_id)}</td>
                  <td><StatePill state={delivery.state} /></td>
                  <td>
                    <Link className="overview-id" to={`/deliveries/${delivery.id}`}>
                      {shortId(delivery.id)}
                    </Link>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </section>
    </div>
  );
}
