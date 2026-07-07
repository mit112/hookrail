import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { LIVE_REFETCH_MS, request } from "../api/client";
import { DLQRow, Page } from "../api/schemas";

export interface DLQFilters {
  endpoint_id?: string;
  replayed?: string;
  since?: string;
  until?: string;
}

export function useDLQ(filters?: DLQFilters, cursor?: string, live = false) {
  const params = new URLSearchParams();
  if (filters?.endpoint_id) params.set("endpoint_id", filters.endpoint_id);
  if (filters?.replayed) params.set("replayed", filters.replayed);
  if (filters?.since) params.set("since", filters.since);
  if (filters?.until) params.set("until", filters.until);
  if (cursor) params.set("cursor", cursor);
  const qs = params.toString();
  return useQuery({
    queryKey: ["dlq", filters?.endpoint_id ?? "", filters?.replayed ?? "", filters?.since ?? "", filters?.until ?? "", cursor ?? ""],
    placeholderData: keepPreviousData,
    refetchInterval: live ? LIVE_REFETCH_MS : false,
    queryFn: () =>
      request("GET", `/v1/dlq${qs ? "?" + qs : ""}`, { schema: Page(DLQRow) }),
  });
}
