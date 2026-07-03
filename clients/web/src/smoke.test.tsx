import { render, screen } from "@testing-library/react";
import { describe, it, expect, beforeAll, afterAll, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { setupServer } from "msw/node";
import { http, HttpResponse } from "msw";
import { App } from "./App";

const server = setupServer(
  http.get("/api/public-config", () => HttpResponse.json({ demo: false })),
  http.get("/api/session", () => HttpResponse.json({ authenticated: true, role: "admin", username: "alice" })),
  http.get("/v1/endpoints", () =>
    HttpResponse.json({
      items: [
        { id: "ep-orders", url: "http://orders-service:9090/succeed", description: "success path", created_at: "2026-01-01T00:00:00Z" },
        { id: "ep-payments", url: "http://payments-service:9090/flap", description: "flaky path", created_at: "2026-01-01T00:00:00Z" },
        { id: "ep-analytics", url: "http://analytics-service:9090/fail/9999", description: "hard-fail path", created_at: "2026-01-01T00:00:00Z" },
      ],
      next_cursor: "",
    }),
  ),
  http.get("/v1/deliveries", () =>
    HttpResponse.json({
      items: [
        { id: "del-1", event_id: "evt-orders", endpoint_id: "ep-orders", state: "succeeded" },
        { id: "del-2", event_id: "evt-payments", endpoint_id: "ep-payments", state: "retry_scheduled" },
        { id: "del-3", event_id: "evt-analytics", endpoint_id: "ep-analytics", state: "dead_lettered" },
      ],
      next_cursor: "",
    }),
  ),
  http.get("/v1/dlq", () =>
    HttpResponse.json({
      items: [
        { delivery_id: "del-3", endpoint_id: "ep-analytics", final_error: "http_500", dead_at: "2026-01-01T00:00:10Z" },
      ],
      next_cursor: "",
    }),
  ),
);
beforeAll(() => server.listen());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

describe("App", () => {
  it("lands on the demo overview with the three delivery outcomes", async () => {
    const qc = new QueryClient();
    render(
      <MemoryRouter>
        <QueryClientProvider client={qc}>
          <App />
        </QueryClientProvider>
      </MemoryRouter>,
    );
    expect(await screen.findByRole("heading", { name: /delivery overview/i }, { timeout: 5000 })).toBeInTheDocument();
    expect(screen.getByText(/successful events land, temporary failures retry, permanent failures go to dead letter/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /overview/i })).toHaveAttribute("aria-current", "page");
    expect(screen.getAllByText("orders-service").length).toBeGreaterThan(0);
    expect(screen.getAllByText("payments-service").length).toBeGreaterThan(0);
    expect(screen.getAllByText("analytics-service").length).toBeGreaterThan(0);
    expect(screen.getByText("http_500")).toBeInTheDocument();
    expect(screen.getAllByText("succeeded").length).toBeGreaterThan(0);
    expect(screen.getAllByText("retry scheduled").length).toBeGreaterThan(0);
    expect(screen.getAllByText("dead lettered").length).toBeGreaterThan(0);
  });
});
