import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { LIVE_REFETCH_MS, request } from "../api/client";
import { EndpointRow, Page } from "../api/schemas";

export function useEndpoints(cursor?: string, live = false) {
  return useQuery({
    queryKey: ["endpoints", cursor ?? ""],
    // Keep the current page visible while the next one loads so the table and
    // "Load more" button don't blank out and reappear mid-fetch.
    placeholderData: keepPreviousData,
    refetchInterval: live ? LIVE_REFETCH_MS : false,
    queryFn: () =>
      request(
        "GET",
        `/v1/endpoints${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ""}`,
        { schema: Page(EndpointRow) },
      ),
  });
}

export function useEndpoint(id: string) {
  return useQuery({
    queryKey: ["endpoints", id],
    queryFn: () => request("GET", `/v1/endpoints/${encodeURIComponent(id)}`, { schema: EndpointRow }),
  });
}
