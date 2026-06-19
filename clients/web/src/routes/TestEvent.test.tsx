import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";
import { describe, it, expect, beforeAll, afterAll, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { setupServer } from "msw/node";
import { http, HttpResponse } from "msw";
import { TestEvent } from "./TestEvent";

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
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    </MemoryRouter>
  );
}

describe("TestEvent form", () => {
  it("renders form with topic input and payload textarea", () => {
    render(<TestEvent />, { wrapper: TestWrapper });
    expect(screen.getByLabelText(/topic/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/payload/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /send/i })).toBeInTheDocument();
  });

  it("shows client error for invalid JSON in payload textarea", async () => {
    render(<TestEvent />, { wrapper: TestWrapper });
    fireEvent.change(screen.getByLabelText(/topic/i), { target: { value: "demo.x" } });
    fireEvent.change(screen.getByLabelText(/payload/i), { target: { value: "not json" } });
    fireEvent.click(screen.getByRole("button", { name: /send/i }));
    await waitFor(() => {
      expect(screen.getByRole("alert")).toBeInTheDocument();
    });
  });

  it("submits valid JSON and shows event_id on success", async () => {
    server.use(
      http.post("/api/test-event", () =>
        HttpResponse.json({ event_id: "ev_1", delivery_ids: ["d1", "d2"] }, { status: 202 }),
      ),
    );
    render(<TestEvent />, { wrapper: TestWrapper });
    fireEvent.change(screen.getByLabelText(/topic/i), { target: { value: "demo.x" } });
    fireEvent.change(screen.getByLabelText(/payload/i), { target: { value: '{"a":1}' } });
    fireEvent.click(screen.getByRole("button", { name: /send/i }));
    await waitFor(() => {
      expect(screen.getByText(/ev_1/)).toBeInTheDocument();
    });
  });

  it("disables submit button while in-flight", async () => {
    server.use(
      http.post("/api/test-event", async () => {
        // never resolve — leave button disabled
        await new Promise(() => {});
      }),
    );
    render(<TestEvent />, { wrapper: TestWrapper });
    fireEvent.change(screen.getByLabelText(/topic/i), { target: { value: "demo.x" } });
    fireEvent.change(screen.getByLabelText(/payload/i), { target: { value: "{}" } });
    fireEvent.click(screen.getByRole("button", { name: /send/i }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /send/i })).toBeDisabled();
    });
  });

  it("shows API error on non-2xx response", async () => {
    server.use(
      http.post("/api/test-event", () =>
        HttpResponse.json(
          { title: "Bad Gateway", status: 502, detail: "upstream unreachable" },
          { status: 502 },
        ),
      ),
    );
    render(<TestEvent />, { wrapper: TestWrapper });
    fireEvent.change(screen.getByLabelText(/topic/i), { target: { value: "demo.x" } });
    fireEvent.change(screen.getByLabelText(/payload/i), { target: { value: "{}" } });
    fireEvent.click(screen.getByRole("button", { name: /send/i }));
    await waitFor(() => {
      expect(screen.getByRole("alert")).toBeInTheDocument();
    });
  });
});
