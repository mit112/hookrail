import { useQuery } from "@tanstack/react-query";
import { request } from "../api/client";
import { EndpointRow, Page } from "../api/schemas";

export function useEndpoints(cursor?: string) {
  return useQuery({
    queryKey: ["endpoints", cursor ?? ""],
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
