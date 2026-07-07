// Resolve an endpoint to a human service name. The demo routes three named
// services (orders/payments/analytics) through one receiver by path, and the
// Overview and Deliveries views both need to show those names instead of raw
// ULIDs — so the mapping lives here as the single source of truth.

// Needles are intentionally distinctive: the demo host alias (orders-service)
// and the demo endpoint description ("success path"). Bare path fragments like
// "/fail" are deliberately excluded — they would mislabel unrelated real
// endpoints (e.g. a "/failover" URL) as a demo service.
export const KNOWN_SERVICES: { name: string; needles: string[] }[] = [
  { name: "orders-service", needles: ["orders-service", "success path"] },
  { name: "payments-service", needles: ["payments-service", "flaky path"] },
  { name: "analytics-service", needles: ["analytics-service", "hard-fail path"] },
];

export interface EndpointLike {
  id: string;
  url: string;
  description: string;
}

export function endpointText(endpoint: EndpointLike): string {
  return `${endpoint.id} ${endpoint.url} ${endpoint.description}`.toLowerCase();
}

// Prefer a known service name, then the URL host, then the description, and
// finally the caller's fallback (usually a short delivery/endpoint id) so the
// table never falls back to an empty cell.
export function serviceForEndpoint(endpoint?: EndpointLike, fallbackId = ""): string {
  if (!endpoint) return fallbackId;
  const text = endpointText(endpoint);
  const known = KNOWN_SERVICES.find((service) => service.needles.some((needle) => text.includes(needle)));
  if (known) return known.name;

  try {
    const host = new URL(endpoint.url).hostname;
    if (host) return host;
  } catch {
    // Keep the table readable even if a stored endpoint URL is malformed.
  }
  return endpoint.description.trim() || endpoint.id || fallbackId;
}
