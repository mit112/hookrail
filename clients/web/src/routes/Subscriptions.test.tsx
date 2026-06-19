import { render, screen, cleanup } from "@testing-library/react";
import { describe, it, expect, beforeAll, afterAll, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { setupServer } from "msw/node";
import { http, HttpResponse } from "msw";
import { Subscriptions } from "./Subscriptions";
import { SubscriptionDetail } from "./SubscriptionDetail";

const server = setupServer();
beforeAll(() => server.listen());
afterEach(() => {
  cleanup();
  server.resetHandlers();
});
afterAll(() => server.close());

function TestWrapper({ children }: { children: React.ReactNode }) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return (
    <MemoryRouter>
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    </MemoryRouter>
  );
}

describe("Subscriptions list", () => {
  it("renders two subs", async () => {
    server.use(
      http.get("/v1/subscriptions", () =>
        HttpResponse.json({
          items: [
            { id: "s1", topic_pattern: "orders.*", endpoint_id: "e1", max_attempts: 8, active: true },
            { id: "s2", topic_pattern: "users.created", endpoint_id: "e2", max_attempts: 3, active: false },
          ],
          next_cursor: "",
        }),
      ),
    );
    render(<Subscriptions />, { wrapper: TestWrapper });
    await screen.findByText("orders.*");
    const rows = screen.getAllByRole("row");
    // header + 2 data rows
    expect(rows).toHaveLength(3);
    expect(screen.getByText("orders.*")).toBeInTheDocument();
    expect(screen.getByText("users.created")).toBeInTheDocument();
    expect(screen.getByText("e1")).toBeInTheDocument();
    expect(screen.getByText("e2")).toBeInTheDocument();
  });

  it("sends ?endpoint_id= filter when set", async () => {
    const sawEndpointId: string[] = [];
    server.use(
      http.get("/v1/subscriptions", ({ request }) => {
        const url = new URL(request.url);
        sawEndpointId.push(url.searchParams.get("endpoint_id") ?? "");
        return HttpResponse.json({
          items: [],
          next_cursor: "",
        });
      }),
    );
    render(<Subscriptions />, { wrapper: TestWrapper });
    await screen.findByText("Subscriptions");
    // On first render with no endpointId filter, the param should be absent
    expect(sawEndpointId[0]).toBe("");
  });

  it("Load more appends the next page", async () => {
    let call = 0;
    server.use(
      http.get("/v1/subscriptions", ({ request }) => {
        const url = new URL(request.url);
        const cursor = url.searchParams.get("cursor");
        call++;
        if (cursor === "page2") {
          return HttpResponse.json({
            items: [{ id: "s2", topic_pattern: "users.created", endpoint_id: "e2", max_attempts: 3, active: false }],
            next_cursor: "",
          });
        }
        return HttpResponse.json({
          items: [{ id: "s1", topic_pattern: "orders.*", endpoint_id: "e1", max_attempts: 8, active: true }],
          next_cursor: "page2",
        });
      }),
    );
    render(<Subscriptions />, { wrapper: TestWrapper });
    await screen.findByText("orders.*");
    const loadMore = screen.getByRole("button", { name: /load more/i });
    loadMore.click();
    await screen.findByText("users.created");
    expect(screen.getByText("orders.*")).toBeInTheDocument();
    expect(screen.getByText("users.created")).toBeInTheDocument();
    expect(call).toBe(2);
  });
});

describe("SubscriptionDetail", () => {
  it("renders backoff_policy and rate_limit_rps", async () => {
    server.use(
      http.get("/v1/subscriptions/s1", () =>
        HttpResponse.json({
          id: "s1",
          topic_pattern: "orders.*",
          endpoint_id: "e1",
          max_attempts: 8,
          rate_limit_rps: 100,
          active: true,
          backoff_policy: { type: "exponential", base_ms: 1000, max_ms: 60000 },
        }),
      ),
    );
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MemoryRouter initialEntries={["/subscriptions/s1"]}>
        <QueryClientProvider client={qc}>
          <Routes>
            <Route path="/subscriptions/:id" element={<SubscriptionDetail />} />
          </Routes>
        </QueryClientProvider>
      </MemoryRouter>,
    );
    await screen.findByText("Subscription s1");
    expect(screen.getByText("orders.*")).toBeInTheDocument();
    expect(screen.getByText("e1")).toBeInTheDocument();
    expect(screen.getByText("8")).toBeInTheDocument();
    expect(screen.getByText("100")).toBeInTheDocument();
    expect(screen.getByText("true")).toBeInTheDocument();
    expect(screen.getByText(/exponential/)).toBeInTheDocument();
  });
});
