import type { z } from "zod";
import { Problem } from "./schemas";

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
