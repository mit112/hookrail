import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";
import { describe, it, expect, beforeAll, afterAll, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { RoleProvider } from "../auth/role";
import { setupServer } from "msw/node";
import { http, HttpResponse } from "msw";
import { DLQ } from "./DLQ";

const server = setupServer();
beforeAll(() => server.listen());
afterEach(() => {
  cleanup();
  server.resetHandlers();
});
afterAll(() => server.close());

function TestWrapper({ children }: { children: React.ReactNode }) {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  return (
    <MemoryRouter>
      <QueryClientProvider client={qc}><RoleProvider role="admin">{children}</RoleProvider></QueryClientProvider>
    </MemoryRouter>
  );
}

describe("DLQ replay", () => {
  it("shows replay button on non-replayed rows and succeeds", async () => {
    server.use(
      http.get("/v1/dlq", () =>
        HttpResponse.json({
          items: [
            {
              delivery_id: "d1",
              endpoint_id: "e1",
              final_error: "connection refused",
              dead_at: "2024-01-01T00:00:00Z",
              replayed_at: undefined,
            },
          ],
          next_cursor: "",
        }),
      ),
      http.post("/v1/dlq/d1/replay", () =>
        HttpResponse.json({ delivery_id: "d1", state: "pending" }),
      ),
    );
    render(<DLQ />, { wrapper: TestWrapper });
    await screen.findByText("d1");

    // Replay button should be visible on non-replayed row
    const replayBtn = screen.getByRole("button", { name: /replay/i });
    expect(replayBtn).toBeInTheDocument();

    fireEvent.click(replayBtn);
    // After successful replay, no error alert is shown
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  });

  it("shows 409 error when not replayable", async () => {
    server.use(
      http.get("/v1/dlq", () =>
        HttpResponse.json({
          items: [
            {
              delivery_id: "d2",
              endpoint_id: "e2",
              final_error: "timeout",
              dead_at: "2024-01-02T00:00:00Z",
              replayed_at: undefined,
            },
          ],
          next_cursor: "",
        }),
      ),
      http.post("/v1/dlq/d2/replay", () =>
        HttpResponse.json(
          { title: "Conflict", status: 409, detail: "delivery not replayable" },
          { status: 409 },
        ),
      ),
    );
    render(<DLQ />, { wrapper: TestWrapper });
    await screen.findByText("d2");

    fireEvent.click(screen.getByRole("button", { name: /replay/i }));
    await waitFor(() => expect(screen.getByText(/409 Conflict/i)).toBeInTheDocument());
  });

  it("shows 410 error when replay window expired", async () => {
    server.use(
      http.get("/v1/dlq", () =>
        HttpResponse.json({
          items: [
            {
              delivery_id: "d3",
              endpoint_id: "e3",
              final_error: "timeout",
              dead_at: "2024-01-02T00:00:00Z",
              replayed_at: undefined,
            },
          ],
          next_cursor: "",
        }),
      ),
      http.post("/v1/dlq/d3/replay", () =>
        HttpResponse.json(
          { title: "Gone", status: 410, detail: "replay window expired" },
          { status: 410 },
        ),
      ),
    );
    render(<DLQ />, { wrapper: TestWrapper });
    await screen.findByText("d3");

    fireEvent.click(screen.getByRole("button", { name: /replay/i }));
    await waitFor(() => expect(screen.getByText(/410 Gone/i)).toBeInTheDocument());
  });

  it("does not show replay button on already-replayed rows", async () => {
    server.use(
      http.get("/v1/dlq", () =>
        HttpResponse.json({
          items: [
            {
              delivery_id: "d4",
              endpoint_id: "e4",
              final_error: "timeout",
              dead_at: "2024-01-02T00:00:00Z",
              replayed_at: "2024-01-03T00:00:00Z",
            },
          ],
          next_cursor: "",
        }),
      ),
    );
    render(<DLQ />, { wrapper: TestWrapper });
    await screen.findByText("d4");

    // No replay button because replayed_at is set
    expect(screen.queryByRole("button", { name: /replay/i })).toBeNull();
  });
});
