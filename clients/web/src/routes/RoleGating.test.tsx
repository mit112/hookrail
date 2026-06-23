import { render, screen, cleanup } from "@testing-library/react";
import { describe, it, expect, beforeAll, afterAll, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { setupServer } from "msw/node";
import { http, HttpResponse } from "msw";
import { Routes, Route, Navigate } from "react-router-dom";
import { RoleProvider, useRole, roleAtLeast, type Role } from "../auth/role";
import { Endpoints } from "./Endpoints";
import { EndpointNew } from "./EndpointForm";
import { DLQ } from "./DLQ";

const server = setupServer(
  http.get("/v1/endpoints", () => HttpResponse.json({ items: [], next_cursor: "" })),
  http.get("/v1/dlq", () =>
    HttpResponse.json({
      items: [{ delivery_id: "d1", endpoint_id: "e1", final_error: "x", dead_at: "2025-01-01T00:00:00Z" }],
      next_cursor: "",
    }),
  ),
);
beforeAll(() => server.listen());
afterEach(() => { cleanup(); server.resetHandlers(); });
afterAll(() => server.close());

function renderAs(role: Role, ui: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter>
      <QueryClientProvider client={qc}>
        <RoleProvider role={role}>{ui}</RoleProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

describe("role-gated controls", () => {
  it("hides '+ New endpoint' for viewer and operator, shows for admin", async () => {
    renderAs("viewer", <Endpoints />);
    await screen.findByText("Endpoints");
    expect(screen.queryByText(/new endpoint/i)).not.toBeInTheDocument();
    cleanup();
    renderAs("operator", <Endpoints />);
    await screen.findByText("Endpoints");
    expect(screen.queryByText(/new endpoint/i)).not.toBeInTheDocument();
    cleanup();
    renderAs("admin", <Endpoints />);
    await screen.findByText("Endpoints");
    expect(screen.getByText(/new endpoint/i)).toBeInTheDocument();
  });

  it("hides DLQ Replay for viewer, shows for operator", async () => {
    renderAs("viewer", <DLQ />);
    await screen.findByText("d1");
    expect(screen.queryByRole("button", { name: /replay/i })).not.toBeInTheDocument();
    cleanup();
    renderAs("operator", <DLQ />);
    await screen.findByText("d1");
    expect(screen.getByRole("button", { name: /replay/i })).toBeInTheDocument();
  });
});

// RoleRoute mirrors App.tsx's route guard for direct-URL access tests.
function RoleRoute({ min, children }: { min: Role; children: React.ReactNode }) {
  return roleAtLeast(useRole(), min) ? <>{children}</> : <Navigate to="/endpoints" replace />;
}

describe("route-level role guards (direct URL)", () => {
  function mountAt(role: Role, path: string) {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return render(
      <MemoryRouter initialEntries={[path]}>
        <QueryClientProvider client={qc}>
          <RoleProvider role={role}>
            <Routes>
              <Route path="/endpoints" element={<div>Endpoints List</div>} />
              <Route path="/endpoints/new" element={<RoleRoute min="admin"><EndpointNew /></RoleRoute>} />
            </Routes>
          </RoleProvider>
        </QueryClientProvider>
      </MemoryRouter>,
    );
  }
  it("redirects a viewer away from /endpoints/new", async () => {
    mountAt("viewer", "/endpoints/new");
    await screen.findByText("Endpoints List");
    expect(screen.queryByRole("button", { name: /create/i })).not.toBeInTheDocument();
  });
  it("lets an admin reach /endpoints/new", async () => {
    mountAt("admin", "/endpoints/new");
    expect(await screen.findByRole("button", { name: /create/i })).toBeInTheDocument();
  });
});
