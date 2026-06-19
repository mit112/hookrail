import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, beforeAll, afterAll, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
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
    defaultOptions: { queries: { retry: false } },
  });
  return (
    <MemoryRouter>
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    </MemoryRouter>
  );
}

describe("DLQ list", () => {
  it("renders two DLQ rows with final_error and dead_at", async () => {
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
            {
              delivery_id: "d2",
              endpoint_id: "e2",
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
    await screen.findByText("d1");
    const rows = screen.getAllByRole("row");
    // header + 2 data rows
    expect(rows).toHaveLength(3);
    expect(screen.getByText("d1")).toBeInTheDocument();
    expect(screen.getByText("e1")).toBeInTheDocument();
    expect(screen.getByText("connection refused")).toBeInTheDocument();
    expect(screen.getByText("d2")).toBeInTheDocument();
    expect(screen.getByText("e2")).toBeInTheDocument();
    expect(screen.getByText("timeout")).toBeInTheDocument();
  });

  it("sends replayed=false filter when filter applied", async () => {
    let capturedReplayed = "";
    let capturedEndpointId = "";
    server.use(
      http.get("/v1/dlq", ({ request }) => {
        const url = new URL(request.url);
        capturedReplayed = url.searchParams.get("replayed") ?? "";
        capturedEndpointId = url.searchParams.get("endpoint_id") ?? "";
        return HttpResponse.json({ items: [], next_cursor: "" });
      }),
    );
    render(<DLQ />, { wrapper: TestWrapper });
    await screen.findByPlaceholderText(/replayed/i);
    const replayedInput = screen.getByPlaceholderText(/replayed/i);
    fireEvent.change(replayedInput, { target: { value: "false" } });
    const endpointInput = screen.getByPlaceholderText(/endpoint_id/i);
    fireEvent.change(endpointInput, { target: { value: "e1" } });
    screen.getByRole("button", { name: /filter/i }).click();
    await waitFor(() => {
      expect(capturedReplayed).toBe("false");
      expect(capturedEndpointId).toBe("e1");
    });
  });
});
