import { useMutation, useQueryClient } from "@tanstack/react-query";
import { z } from "zod";
import { request } from "../api/client";

const CreateEndpointResponse = z.object({
  id: z.string(),
  url: z.string(),
  secret: z.string(),
});

export function useCreateEndpoint() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { url: string; description?: string }) =>
      request("POST", "/v1/endpoints", { body, schema: CreateEndpointResponse }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["endpoints"] }),
    retry: false,
  });
}

export function useUpdateEndpoint(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Record<string, unknown>) =>
      request("PATCH", `/v1/endpoints/${encodeURIComponent(id)}`, { body }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["endpoints"] }),
    retry: false,
  });
}

export function useDeleteEndpoint() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      request("DELETE", `/v1/endpoints/${encodeURIComponent(id)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["endpoints"] }),
    retry: false,
  });
}
