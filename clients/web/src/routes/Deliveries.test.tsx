import { render, screen, cleanup, fireEvent, waitFor, within } from "@testing-library/react";
import { describe, it, expect, beforeAll, afterAll, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { setupServer } from "msw/node";
import { http, HttpResponse } from "msw";
import { Deliveries } from "./Deliveries";
import { DeliveryTimeline } from "./DeliveryTimeline";

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

describe("Deliveries list", () => {
  it("renders two deliveries with state from the API", async () => {
    server.use(
      http.get("/v1/deliveries", () =>
        HttpResponse.json({
          items: [
            { id: "d1", event_id: "ev1", endpoint_id: "e1", state: "succeeded" },
            { id: "d2", event_id: "ev2", endpoint_id: "e2", state: "pending" },
          ],
          next_cursor: "",
        }),
      ),
    );
    render(<Deliveries />, { wrapper: TestWrapper });
    await screen.findByText("d1");
    const rows = screen.getAllByRole("row");
    // header + 2 data rows
    expect(rows).toHaveLength(3);
    // Scope state assertions to the table: the filter <select> also lists these
    // state names as options.
    const table = within(screen.getByRole("table"));
    expect(screen.getByText("d1")).toBeInTheDocument();
    expect(screen.getByText("ev1")).toBeInTheDocument();
    expect(table.getByText("succeeded")).toBeInTheDocument();
    expect(screen.getByText("d2")).toBeInTheDocument();
    expect(screen.getByText("ev2")).toBeInTheDocument();
    expect(table.getByText("pending")).toBeInTheDocument();
  });

  it("sends state filter query parameter", async () => {
    let capturedState = "";
    server.use(
      http.get("/v1/deliveries", ({ request }) => {
        const url = new URL(request.url);
        capturedState = url.searchParams.get("state") ?? "";
        return HttpResponse.json({ items: [], next_cursor: "" });
      }),
    );
    render(<Deliveries />, { wrapper: TestWrapper });
    // Pick a state from the select and apply the filter.
    const stateSelect = await screen.findByLabelText(/state/i);
    fireEvent.change(stateSelect, { target: { value: "succeeded" } });
    screen.getByRole("button", { name: /filter/i }).click();
    // After filter, the query should include ?state=succeeded
    await waitFor(() => {
      expect(capturedState).toBe("succeeded");
    });
  });
});

describe("DeliveryTimeline", () => {
  it("renders timeline with two attempts and attempts_truncated", async () => {
    server.use(
      http.get("/v1/deliveries/d1", () =>
        HttpResponse.json({
          delivery_id: "d1",
          state: "succeeded",
          attempts_truncated: false,
          attempts: [
            {
              attempt_no: 1,
              claim_version: 0,
              status: "delivered",
              http_status: 200,
              error_class: undefined,
              latency_ms: 42,
            },
            {
              attempt_no: 2,
              claim_version: 0,
              status: "delivered",
              http_status: 200,
              error_class: undefined,
              latency_ms: 55,
            },
          ],
        }),
      ),
    );
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MemoryRouter initialEntries={["/deliveries/d1"]}>
        <QueryClientProvider client={qc}>
          <Routes>
            <Route path="/deliveries/:id" element={<DeliveryTimeline />} />
          </Routes>
        </QueryClientProvider>
      </MemoryRouter>,
    );
    await screen.findByText(/delivery d1/i);
    expect(screen.getByText(/succeeded/)).toBeInTheDocument();
    // Two attempt rows
    expect(screen.getByText("1")).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();
    expect(screen.getAllByText("200")).toHaveLength(2);
    expect(screen.getByText("42")).toBeInTheDocument();
    expect(screen.getByText("55")).toBeInTheDocument();
  });

  it("shows attempts_truncated badge when true", async () => {
    server.use(
      http.get("/v1/deliveries/d2", () =>
        HttpResponse.json({
          delivery_id: "d2",
          state: "dead_lettered",
          attempts_truncated: true,
          attempts: [
            { attempt_no: 1, claim_version: 0, status: "failed", http_status: 500, error_class: undefined, latency_ms: 1000 },
          ],
        }),
      ),
    );
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MemoryRouter initialEntries={["/deliveries/d2"]}>
        <QueryClientProvider client={qc}>
          <Routes>
            <Route path="/deliveries/:id" element={<DeliveryTimeline />} />
          </Routes>
        </QueryClientProvider>
      </MemoryRouter>,
    );
    await screen.findByText(/delivery d2/i);
    expect(screen.getByText(/truncated/i)).toBeInTheDocument();
  });
});
