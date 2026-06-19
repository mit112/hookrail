import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";
import { describe, it, expect, vi, beforeAll, afterAll, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { setupServer } from "msw/node";
import { http, HttpResponse } from "msw";
import { SubscriptionForm } from "./SubscriptionForm";

const server = setupServer();
beforeAll(() => server.listen());
afterEach(() => {
  cleanup();
  server.resetHandlers();
});
afterAll(() => server.close());

function TestWrapper({ children }: { children: React.ReactNode }) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return (
    <MemoryRouter>
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    </MemoryRouter>
  );
}

const validSubscription = {
  id: "sub_1",
  topic_pattern: "orders.*",
  endpoint_id: "e1",
  max_attempts: 3,
  active: true,
};

describe("SubscriptionForm create", () => {
  it("submits create with zod-validated fields", async () => {
    let capturedBody: unknown = null;
    server.use(
      http.post("/v1/subscriptions", async ({ request }) => {
        capturedBody = await request.json();
        expect(request.headers.get("Content-Type")).toBe("application/json");
        return HttpResponse.json({ id: "sub_1" }, { status: 201 });
      }),
    );
    const onSuccess = vi.fn();
    render(<SubscriptionForm onSuccess={onSuccess} />, { wrapper: TestWrapper });

    fireEvent.change(screen.getByLabelText(/topic/i), { target: { value: "orders.*" } });
    fireEvent.change(screen.getByLabelText(/endpoint/i), { target: { value: "e1" } });
    fireEvent.change(screen.getByLabelText(/max attempts/i), { target: { value: "8" } });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));

    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
    expect(capturedBody).toEqual({ topic_pattern: "orders.*", endpoint_id: "e1", max_attempts: 8 });
  });

  it("shows client-side error when max_attempts is 0", async () => {
    // No MSW handler registered — any POST would throw
    render(<SubscriptionForm onSuccess={vi.fn()} />, { wrapper: TestWrapper });

    fireEvent.change(screen.getByLabelText(/topic/i), { target: { value: "orders.*" } });
    fireEvent.change(screen.getByLabelText(/endpoint/i), { target: { value: "e1" } });
    fireEvent.change(screen.getByLabelText(/max attempts/i), { target: { value: "0" } });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));

    await screen.findByText(/max_attempts/i);
    expect(screen.getByText(/max_attempts/i)).toBeInTheDocument();
  });

  it("shows client-side error when rate_limit_rps <= 0", async () => {
    render(<SubscriptionForm onSuccess={vi.fn()} />, { wrapper: TestWrapper });

    fireEvent.change(screen.getByLabelText(/topic/i), { target: { value: "orders.*" } });
    fireEvent.change(screen.getByLabelText(/endpoint/i), { target: { value: "e1" } });
    fireEvent.change(screen.getByLabelText(/max attempts/i), { target: { value: "5" } });
    fireEvent.change(screen.getByLabelText(/rate limit/i), { target: { value: "0" } });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));

    await screen.findByText(/rate_limit_rps/i);
    expect(screen.getByText(/rate_limit_rps/i)).toBeInTheDocument();
  });

  it("shows inline error on 422", async () => {
    server.use(
      http.post("/v1/subscriptions", () =>
        HttpResponse.json(
          { title: "Unprocessable Entity", status: 422, detail: "topic_pattern must match regex" },
          { status: 422 },
        ),
      ),
    );
    render(<SubscriptionForm onSuccess={vi.fn()} />, { wrapper: TestWrapper });

    fireEvent.change(screen.getByLabelText(/topic/i), { target: { value: "bad[[regex" } });
    fireEvent.change(screen.getByLabelText(/endpoint/i), { target: { value: "e1" } });
    fireEvent.change(screen.getByLabelText(/max attempts/i), { target: { value: "5" } });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));

    await waitFor(() => expect(screen.getByText("topic_pattern must match regex")).toBeInTheDocument());
  });

  it("shows inline error on 409", async () => {
    server.use(
      http.post("/v1/subscriptions", () =>
        HttpResponse.json(
          { title: "Conflict", status: 409, detail: "endpoint deleted" },
          { status: 409 },
        ),
      ),
    );
    render(<SubscriptionForm onSuccess={vi.fn()} />, { wrapper: TestWrapper });

    fireEvent.change(screen.getByLabelText(/endpoint/i), { target: { value: "e_deleted" } });
    fireEvent.change(screen.getByLabelText(/topic/i), { target: { value: "orders.*" } });
    fireEvent.change(screen.getByLabelText(/max attempts/i), { target: { value: "5" } });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));

    await waitFor(() => {
      expect(screen.getByText(/endpoint not available|deleted/i)).toBeInTheDocument();
    });
  });
});

describe("SubscriptionForm edit", () => {
  it("submits PATCH with changed fields", async () => {
    let capturedBody: unknown = null;
    server.use(
      http.patch("/v1/subscriptions/sub_1", async ({ request }) => {
        capturedBody = await request.json();
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const onSuccess = vi.fn();
    render(
      <SubscriptionForm subscription={validSubscription} onSuccess={onSuccess} />,
      { wrapper: TestWrapper },
    );

    fireEvent.change(screen.getByLabelText(/max attempts/i), { target: { value: "5" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));

    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
    expect(capturedBody).toEqual({ max_attempts: 5 });
  });
});

describe("SubscriptionForm delete", () => {
  it("shows confirm and deletes", async () => {
    let deleteCalled = false;
    server.use(
      http.delete("/v1/subscriptions/sub_1", () => {
        deleteCalled = true;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const onSuccess = vi.fn();
    render(
      <SubscriptionForm subscription={validSubscription} onSuccess={onSuccess} />,
      { wrapper: TestWrapper },
    );

    fireEvent.click(screen.getByRole("button", { name: /delete/i }));
    await screen.findByText(/are you sure/i);
    fireEvent.click(screen.getByRole("button", { name: /confirm/i }));

    await waitFor(() => expect(deleteCalled).toBe(true));
    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
  });
});
