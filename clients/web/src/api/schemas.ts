import { z } from "zod";

export const DeliveryState = z.enum([
  "pending", "in_flight", "retry_scheduled", "succeeded", "dead_lettered", "cancelled", "skipped",
]);

export const EndpointRow = z.object({
  id: z.string(), url: z.string(), description: z.string(),
  created_at: z.string(), deleted_at: z.string().optional(),
});
export const SubscriptionRow = z.object({
  id: z.string(), topic_pattern: z.string(), endpoint_id: z.string(),
  max_attempts: z.number(), rate_limit_rps: z.number().optional(),
  backoff_policy: z.unknown().optional(), active: z.boolean(), deleted_at: z.string().optional(),
});
export const DeliveryListRow = z.object({
  id: z.string(), event_id: z.string(), endpoint_id: z.string(), state: DeliveryState,
});
export const AttemptView = z.object({
  attempt_no: z.number(), claim_version: z.number(), status: z.string(),
  http_status: z.number().optional(), error_class: z.string().optional(), latency_ms: z.number(),
});
export const DeliveryTimeline = z.object({
  delivery_id: z.string(), state: DeliveryState,
  attempts_truncated: z.boolean(), attempts: z.array(AttemptView),
});
export const DLQRow = z.object({
  delivery_id: z.string(), endpoint_id: z.string().optional(),
  final_error: z.string(), dead_at: z.string(), replayed_at: z.string().optional(),
});
export const Problem = z.object({
  type: z.string().optional(), title: z.string(), status: z.number(), detail: z.string().optional(),
});
export const Page = <T extends z.ZodTypeAny>(item: T) =>
  z.object({ items: z.array(item).nullable().transform((v) => v ?? []), next_cursor: z.string() });

export type TEndpointRow = z.infer<typeof EndpointRow>;
export type TSubscriptionRow = z.infer<typeof SubscriptionRow>;
export type TDeliveryListRow = z.infer<typeof DeliveryListRow>;
export type TDeliveryTimeline = z.infer<typeof DeliveryTimeline>;
export type TDLQRow = z.infer<typeof DLQRow>;
