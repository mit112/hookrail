import { useMutation, useQueryClient } from "@tanstack/react-query";
import { z } from "zod";
import { request } from "../api/client";

const CreateSubscriptionResponse = z.object({ id: z.string() });

// Zod schema for client-side validation (mirrors plan Step 3)
export const CreateInput = z.object({
  topic_pattern: z.string().min(1, "topic_pattern is required"),
  endpoint_id: z.string().min(1, "endpoint_id is required"),
  max_attempts: z.number().int().min(1, "max_attempts must be at least 1").max(100),
  rate_limit_rps: z.number().positive("rate_limit_rps must be positive").optional(),
  backoff_policy: z.object({}).passthrough().optional(),
});

export function useCreateSubscription() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: z.infer<typeof CreateInput>) =>
      request("POST", "/v1/subscriptions", { body, schema: CreateSubscriptionResponse }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["subscriptions"] }),
    retry: false,
  });
}

export function useUpdateSubscription(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Record<string, unknown>) =>
      request("PATCH", `/v1/subscriptions/${encodeURIComponent(id)}`, { body }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["subscriptions"] }),
    retry: false,
  });
}

export function useDeleteSubscription() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      request("DELETE", `/v1/subscriptions/${encodeURIComponent(id)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["subscriptions"] }),
    retry: false,
  });
}
