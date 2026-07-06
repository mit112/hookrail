import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { LIVE_REFETCH_MS, request } from "../api/client";
import { DeliveryListRow, DeliveryTimeline, Page } from "../api/schemas";

export interface DeliveryFilters {
  state?: string;
  endpoint_id?: string;
  topic?: string;
  event_id?: string;
}

// `live` polls on an interval; only the Overview enables it. The paginated
// Deliveries page leaves it off because its "Load more" accumulator does not
// compose with background refetches.
export function useDeliveries(filters?: DeliveryFilters, cursor?: string, live = false) {
  const params = new URLSearchParams();
  if (filters?.state) params.set("state", filters.state);
  if (filters?.endpoint_id) params.set("endpoint_id", filters.endpoint_id);
  if (filters?.topic) params.set("topic", filters.topic);
  if (filters?.event_id) params.set("event_id", filters.event_id);
  if (cursor) params.set("cursor", cursor);
  const qs = params.toString();
  return useQuery({
    queryKey: ["deliveries", filters?.state ?? "", filters?.endpoint_id ?? "", filters?.topic ?? "", filters?.event_id ?? "", cursor ?? ""],
    placeholderData: keepPreviousData,
    refetchInterval: live ? LIVE_REFETCH_MS : false,
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
