import { Link } from "react-router-dom";
import { StatePill } from "../components/StatePill";
import type { TDeliveryListRow } from "../api/schemas";
import { serviceForEndpoint } from "../lib/endpointName";
import { useDeliveries } from "../query/deliveries";
import { useDLQ } from "../query/dlq";
import { useEndpoints } from "../query/endpoints";

type Bucket = "delivered" | "retrying" | "dead";

type Lane = {
  key: "orders" | "payments" | "analytics";
  title: string;
  service: string;
  state: TDeliveryListRow["state"];
  bucket: Bucket;
  path: string;
  copy: string;
};

const LANES: Lane[] = [
  {
    key: "orders",
    title: "Delivering",
    service: "orders-service",
    state: "succeeded",
    bucket: "delivered",
    path: "success path",
    copy: "Successful events land after the receiver accepts the webhook.",
  },
  {
    key: "payments",
    title: "Retrying",
    service: "payments-service",
    state: "retry_scheduled",
    bucket: "retrying",
    path: "flaky path",
    copy: "Temporary failures stay visible while the next retry is scheduled.",
  },
  {
    key: "analytics",
    title: "Dead-lettered",
    service: "analytics-service",
    state: "dead_lettered",
    bucket: "dead",
    path: "hard-fail path",
    copy: "Permanent failures stop retrying and move to dead letter for review.",
  },
];

const BUCKET_UNIT: Record<Bucket, string> = {
  delivered: "delivered",
  retrying: "retrying",
  dead: "in dead letter",
};

function shortId(id: string): string {
  return id.length > 18 ? `${id.slice(0, 12)}...` : id;
}

function bucketOf(state: TDeliveryListRow["state"]): Bucket | "other" {
  if (state === "succeeded") return "delivered";
  if (state === "dead_lettered") return "dead";
  if (state === "pending" || state === "in_flight" || state === "retry_scheduled") return "retrying";
  return "other";
}

// Global mix of the recent delivery window — feeds the ribbon. "retrying" folds
// the in-progress states since a viewer reads them all as "still working."
function tallyStates(deliveries: TDeliveryListRow[]) {
  let delivered = 0;
  let retrying = 0;
  let dead = 0;
  for (const d of deliveries) {
    const b = bucketOf(d.state);
    if (b === "delivered") delivered += 1;
    else if (b === "retrying") retrying += 1;
    else if (b === "dead") dead += 1;
  }
  const total = deliveries.length;
  return { delivered, retrying, dead, other: total - delivered - retrying - dead, total };
}

const RIBBON = [
  { key: "delivered", label: "Delivered", cls: "ok" },
  { key: "retrying", label: "Retrying", cls: "active" },
  { key: "dead", label: "Dead-lettered", cls: "bad" },
  { key: "other", label: "Other", cls: "neutral" },
] as const;

export function Overview() {
  const endpoints = useEndpoints(undefined, true);
  const deliveries = useDeliveries(undefined, undefined, true);
  const dlq = useDLQ(undefined, undefined, true);

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
  const tally = tallyStates(deliveryItems);
  const ribbonCounts: Record<string, number> = tally;
  const dlqHasMore = Boolean(dlq.data?.next_cursor);

  // Which demo service an endpoint id belongs to (empty string => not a lane).
  const serviceOf = (endpointId?: string) =>
    endpointId ? serviceForEndpoint(endpointsById.get(endpointId), "") : "";

  // Per-service counts so a card's number matches its service label: delivered
  // and retrying come from the recent delivery window for that service; the
  // dead-lettered card counts that service's rows on the current DLQ page.
  const laneCount = (lane: Lane): number =>
    lane.bucket === "dead"
      ? dlqItems.filter((row) => serviceOf(row.endpoint_id) === lane.service).length
      : deliveryItems.filter(
          (d) => serviceOf(d.endpoint_id) === lane.service && bucketOf(d.state) === lane.bucket,
        ).length;

  const analyticsError =
    dlqItems.find((row) => serviceOf(row.endpoint_id) === "analytics-service")?.final_error ?? "http_500";

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

      <section className="pulse" aria-label="Recent delivery mix">
        <div className="pulse-head">
          <span className="pulse-title">Delivery mix</span>
          <span className="pulse-window">
            {tally.total > 0 ? `last ${tally.total} deliveries` : "waiting for traffic"}
          </span>
        </div>
        <div
          className="pulse-bar"
          role="img"
          aria-label={`Of the last ${tally.total} deliveries: ${tally.delivered} delivered, ${tally.retrying} retrying, ${tally.dead} dead-lettered`}
        >
          {tally.total > 0 &&
            RIBBON.map((seg) => {
              const n = ribbonCounts[seg.key] ?? 0;
              return n > 0 ? (
                <span
                  key={seg.key}
                  className={`pulse-seg pulse-seg--${seg.cls}`}
                  style={{ flexGrow: n }}
                />
              ) : null;
            })}
        </div>
        <ul className="pulse-legend">
          {RIBBON.filter((seg) => seg.key !== "other" || tally.other > 0).map((seg) => (
            <li key={seg.key} className={`pulse-key pulse-key--${seg.cls}`}>
              <span className="pulse-dot" aria-hidden="true" />
              {seg.label}
              <b>{ribbonCounts[seg.key] ?? 0}</b>
            </li>
          ))}
        </ul>
      </section>

      <section className="overview-panels" aria-label="Delivery outcomes">
        {LANES.map((lane) => {
          const endpoint = endpointItems.find((e) => serviceForEndpoint(e) === lane.service);
          const count = laneCount(lane);
          const countLabel = lane.bucket === "dead" && dlqHasMore ? `${count}+` : `${count}`;

          return (
            <article className={`overview-panel overview-panel--${lane.key}`} key={lane.key}>
              <div className="overview-panel__top">
                <span className="overview-panel__title">{lane.title}</span>
                <StatePill state={lane.state} />
              </div>
              <h2>{endpoint ? serviceForEndpoint(endpoint) : lane.service}</h2>
              <p>{lane.copy}</p>
              <div className="overview-panel__metric">
                <span className="overview-panel__count">{countLabel}</span>
                <span className="overview-panel__unit">{BUCKET_UNIT[lane.bucket]}</span>
              </div>
              <dl>
                <div>
                  <dt>Route</dt>
                  <dd>{lane.path}</dd>
                </div>
                {lane.bucket === "dead" && (
                  <div>
                    <dt>Final error</dt>
                    <dd>{analyticsError}</dd>
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
        <table className="overview-table data-cards">
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
                  <td data-label="Endpoint">
                    <span className="overview-service">
                      {serviceForEndpoint(endpoint, delivery.endpoint_id)}
                    </span>
                  </td>
                  <td className="overview-id" data-label="Event">{shortId(delivery.event_id)}</td>
                  <td data-label="State"><StatePill state={delivery.state} /></td>
                  <td data-label="Delivery">
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
