import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { request } from "../api/client";
import { EndpointRow, Page } from "../api/schemas";

export function useEndpoints(cursor?: string) {
  return useQuery({
    queryKey: ["endpoints", cursor ?? ""],
    // Keep the current page visible while the next one loads so the table and
    // "Load more" button don't blank out and reappear mid-fetch.
    placeholderData: keepPreviousData,
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
