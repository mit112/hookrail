import { useQuery } from "@tanstack/react-query";
import { request } from "../api/client";
import { DeliveryListRow, DeliveryTimeline, Page } from "../api/schemas";

export interface DeliveryFilters {
  state?: string;
  endpoint_id?: string;
  topic?: string;
  event_id?: string;
}

export function useDeliveries(filters?: DeliveryFilters, cursor?: string) {
  const params = new URLSearchParams();
  if (filters?.state) params.set("state", filters.state);
  if (filters?.endpoint_id) params.set("endpoint_id", filters.endpoint_id);
  if (filters?.topic) params.set("topic", filters.topic);
  if (filters?.event_id) params.set("event_id", filters.event_id);
  if (cursor) params.set("cursor", cursor);
  const qs = params.toString();
  return useQuery({
    queryKey: ["deliveries", filters?.state ?? "", filters?.endpoint_id ?? "", filters?.topic ?? "", filters?.event_id ?? "", cursor ?? ""],
    queryFn: () =>
      request("GET", `/v1/deliveries${qs ? "?" + qs : ""}`, { schema: Page(DeliveryListRow) }),
  });
}

export function useDeliveryTimeline(id: string) {
  return useQuery({
    queryKey: ["deliveries", id],
    queryFn: () => request("GET", `/v1/deliveries/${encodeURIComponent(id)}`, { schema: DeliveryTimeline }),
  });
}
