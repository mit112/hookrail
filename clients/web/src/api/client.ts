import type { z } from "zod";
import { Problem } from "./schemas";

// Poll interval for the live Overview so it updates on its own without a manual
// refresh. React Query pauses this while the tab is hidden. Only the Overview
// opts in (via the `live` flag on the list hooks) — the paginated list pages
// accumulate pages behind "Load more", which does not compose with polling.
//
// Cost: the Overview issues three list queries, so ~45 req/min per visible tab.
// That is well under the per-session read cap (300/min), but note the global
// authenticated-read cap is 3000/min (internal/dashboard/auth.go): past ~65
// simultaneously-visible Overview tabs, visitors would start sharing 429s. The
// failure is graceful (React Query retries), acceptable for a demo.
export const LIVE_REFETCH_MS = 4000;

export class ApiProblem extends Error {
  constructor(public status: number, public title: string, public detail?: string) {
    super(`${status} ${title}`);
  }
}

export async function request<T>(
  method: string,
  path: string,
  opts: { schema?: z.ZodType<T>; body?: unknown } = {},
): Promise<T> {
  const isMutation = method === "POST" || method === "PATCH" || method === "DELETE";
  const res = await fetch(path, {
    method,
    credentials: "include",
    headers: opts.body !== undefined || isMutation ? { "Content-Type": "application/json" } : {},
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  });
  if (!res.ok) {
    let p: z.infer<typeof Problem> = { title: res.statusText, status: res.status };
    try { p = Problem.parse(await res.json()); } catch { /* non-problem body */ }
    throw new ApiProblem(p.status, p.title, p.detail);
  }
  if (res.status === 204) return undefined as T;
  const json = await res.json();
  return opts.schema ? opts.schema.parse(json) : (json as T);
}
