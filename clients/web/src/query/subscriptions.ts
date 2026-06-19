import { useQuery } from "@tanstack/react-query";
import { request } from "../api/client";
import { SubscriptionRow, Page } from "../api/schemas";

export function useSubscriptions(endpointId?: string, cursor?: string) {
  const params = new URLSearchParams();
  if (endpointId) params.set("endpoint_id", endpointId);
  if (cursor) params.set("cursor", cursor);
  const qs = params.toString();
  return useQuery({
    queryKey: ["subscriptions", endpointId ?? "", cursor ?? ""],
    queryFn: () =>
      request("GET", `/v1/subscriptions${qs ? "?" + qs : ""}`, { schema: Page(SubscriptionRow) }),
  });
}

export function useSubscription(id: string) {
  return useQuery({
    queryKey: ["subscriptions", id],
    queryFn: () => request("GET", `/v1/subscriptions/${encodeURIComponent(id)}`, { schema: SubscriptionRow }),
  });
}
