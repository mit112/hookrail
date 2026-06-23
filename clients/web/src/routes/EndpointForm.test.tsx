import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";
import { describe, it, expect, vi, beforeAll, afterAll, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { RoleProvider } from "../auth/role";
import { setupServer } from "msw/node";
import { http, HttpResponse } from "msw";
import { EndpointForm, EndpointNew } from "./EndpointForm";

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
      <QueryClientProvider client={qc}><RoleProvider role="admin">{children}</RoleProvider></QueryClientProvider>
    </MemoryRouter>
  );
}

describe("EndpointForm create", () => {
  it("submits create and calls onSecret with the returned secret", async () => {
    let capturedBody: unknown = null;
    server.use(
      http.post("/v1/endpoints", async ({ request }) => {
        capturedBody = await request.json();
        expect(request.headers.get("Content-Type")).toBe("application/json");
        return HttpResponse.json(
          { id: "e1", url: "https://x", secret: "whsec_abc" },
          { status: 201 },
        );
      }),
    );
    const onSecret = vi.fn();
    render(<EndpointForm onSecret={onSecret} />, { wrapper: TestWrapper });

    fireEvent.change(screen.getByLabelText(/url/i), { target: { value: "https://x" } });
    fireEvent.change(screen.getByLabelText(/description/i), { target: { value: "d" } });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));

    await waitFor(() => expect(onSecret).toHaveBeenCalledWith("whsec_abc"));
    expect(capturedBody).toEqual({ url: "https://x", description: "d" });
  });

  it("shows inline error on 422 SSRF", async () => {
    server.use(
      http.post("/v1/endpoints", () =>
        HttpResponse.json(
          { title: "Unprocessable Entity", status: 422, detail: "SSRF blocked" },
          { status: 422 },
        ),
      ),
    );
    render(<EndpointForm onSecret={vi.fn()} />, { wrapper: TestWrapper });

    fireEvent.change(screen.getByLabelText(/url/i), { target: { value: "https://blocked" } });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));

    await waitFor(() => expect(screen.getByText("SSRF blocked")).toBeInTheDocument());
  });

  it("shows client error when url is empty on submit", async () => {
    render(<EndpointForm onSecret={vi.fn()} />, { wrapper: TestWrapper });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));
    // Should show validation error inline, not make a network request
    await screen.findByText(/url is required/i);
  });
});

describe("EndpointForm edit", () => {
  it("submits PATCH and calls onSuccess", async () => {
    let capturedBody: unknown = null;
    let capturedMethod = "";
    server.use(
      http.patch("/v1/endpoints/e1", async ({ request }) => {
        capturedMethod = request.method;
        capturedBody = await request.json();
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const onSuccess = vi.fn();
    render(
      <EndpointForm
        endpoint={{ id: "e1", url: "https://old", description: "old desc", created_at: "t" }}
        onSuccess={onSuccess}
      />,
      { wrapper: TestWrapper },
    );

    // Form should be pre-filled with existing url
    expect(screen.getByLabelText(/url/i)).toHaveValue("https://old");

    fireEvent.change(screen.getByLabelText(/url/i), { target: { value: "https://new" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));

    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
    expect(capturedMethod).toBe("PATCH");
    expect(capturedBody).toEqual({ url: "https://new" });
  });
});

describe("EndpointForm delete", () => {
  it("shows confirm and deletes on confirmation", async () => {
    let deleteCalled = false;
    server.use(
      http.delete("/v1/endpoints/e1", () => {
        deleteCalled = true;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const onSuccess = vi.fn();
    render(
      <EndpointForm
        endpoint={{ id: "e1", url: "https://x", description: "d", created_at: "t" }}
        onSuccess={onSuccess}
      />,
      { wrapper: TestWrapper },
    );

    fireEvent.click(screen.getByRole("button", { name: /delete/i }));
    await screen.findByText(/are you sure/i);
    fireEvent.click(screen.getByRole("button", { name: /confirm/i }));

    await waitFor(() => expect(deleteCalled).toBe(true));
    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
  });
});

describe("EndpointNew create + reveal", () => {
  it("clears the secret from the DOM when the reveal modal is closed", async () => {
    server.use(
      http.post("/v1/endpoints", async ({ request }) => {
        expect(request.headers.get("Content-Type")).toBe("application/json");
        return HttpResponse.json(
          { id: "e1", url: "https://x", secret: "whsec_create" },
          { status: 201 },
        );
      }),
    );
    render(<EndpointNew />, { wrapper: TestWrapper });

    fireEvent.change(screen.getByLabelText(/url/i), { target: { value: "https://x" } });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));

    // Secret should appear in the reveal modal
    await screen.findByText("whsec_create");

    // Click "I saved it" to close
    fireEvent.click(screen.getByRole("button", { name: /i saved it/i }));

    // Secret should be gone from DOM — the mutation result was cleared
    await waitFor(() => expect(screen.queryByText("whsec_create")).not.toBeInTheDocument());
  });
});
