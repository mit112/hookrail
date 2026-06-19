import { it, expect, beforeAll, afterAll, afterEach } from "vitest";
import { setupServer } from "msw/node";
import { http, HttpResponse } from "msw";
import { request, ApiProblem } from "./client";
import { EndpointRow } from "./schemas";

const server = setupServer();
beforeAll(() => server.listen());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

it("parses a 2xx body with the schema", async () => {
  server.use(http.get("/v1/endpoints/e1", () =>
    HttpResponse.json({ id: "e1", url: "https://x", description: "", created_at: "t" })));
  const e = await request("GET", "/v1/endpoints/e1", { schema: EndpointRow });
  expect(e.id).toBe("e1");
});

it("throws ApiProblem on non-2xx", async () => {
  server.use(http.get("/v1/endpoints/missing", () =>
    HttpResponse.json({ title: "not found", status: 404 }, { status: 404 })));
  await expect(request("GET", "/v1/endpoints/missing", { schema: EndpointRow }))
    .rejects.toBeInstanceOf(ApiProblem);
});

it("sends application/json on a no-body mutation (CSRF middleware needs it)", async () => {
  let ct: string | null = "";
  server.use(http.post("/api/logout", ({ request }) => {
    ct = request.headers.get("content-type");
    return new HttpResponse(null, { status: 204 });
  }));
  await request("POST", "/api/logout");
  expect(ct).toBe("application/json");
});
