import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";
import { describe, it, expect, beforeAll, afterAll, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { setupServer } from "msw/node";
import { http, HttpResponse } from "msw";
import { Endpoints } from "./Endpoints";
import { EndpointDetail } from "./EndpointDetail";

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

describe("Endpoints list", () => {
  it("renders two endpoints from the API", async () => {
    server.use(
      http.get("/v1/endpoints", () =>
        HttpResponse.json({
          items: [
            { id: "e1", url: "https://a.example", description: "First", created_at: "2025-01-01T00:00:00Z" },
            { id: "e2", url: "https://b.example", description: "Second", created_at: "2025-01-02T00:00:00Z" },
          ],
          next_cursor: "",
        }),
      ),
    );
    render(<Endpoints />, { wrapper: TestWrapper });
    await screen.findByText("https://a.example");
    const rows = screen.getAllByRole("row");
    // header + 2 data rows
    expect(rows).toHaveLength(3);
    expect(screen.getByText("https://a.example")).toBeInTheDocument();
    expect(screen.getByText("https://b.example")).toBeInTheDocument();
    expect(screen.getByText("First")).toBeInTheDocument();
    expect(screen.getByText("Second")).toBeInTheDocument();
  });

  it("shows Load more when next_cursor is present", async () => {
    server.use(
      http.get("/v1/endpoints", () =>
        HttpResponse.json({
          items: [{ id: "e1", url: "https://a.example", description: "", created_at: "t" }],
          next_cursor: "page2",
        }),
      ),
    );
    render(<Endpoints />, { wrapper: TestWrapper });
    await screen.findByText("https://a.example");
    expect(screen.getByRole("button", { name: /load more/i })).toBeInTheDocument();
  });

  it("Load more appends the next page", async () => {
    let call = 0;
    server.use(
      http.get("/v1/endpoints", ({ request }) => {
        const url = new URL(request.url);
        const cursor = url.searchParams.get("cursor");
        call++;
        if (cursor === "page2") {
          return HttpResponse.json({
            items: [{ id: "e2", url: "https://b.example", description: "", created_at: "t" }],
            next_cursor: "",
          });
        }
        return HttpResponse.json({
          items: [{ id: "e1", url: "https://a.example", description: "", created_at: "t" }],
          next_cursor: "page2",
        });
      }),
    );
    render(<Endpoints />, { wrapper: TestWrapper });
    await screen.findByText("https://a.example");
    const loadMore = screen.getByRole("button", { name: /load more/i });
    loadMore.click();
    await screen.findByText("https://b.example");
    expect(screen.getByText("https://a.example")).toBeInTheDocument();
    expect(screen.getByText("https://b.example")).toBeInTheDocument();
    expect(call).toBe(2);
  });
});

describe("EndpointDetail", () => {
  it("renders endpoint detail", async () => {
    server.use(
      http.get("/v1/endpoints/e1", () =>
        HttpResponse.json({
          id: "e1",
          url: "https://a.example",
          description: "desc",
          created_at: "2025-01-01T00:00:00Z",
        }),
      ),
    );
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MemoryRouter initialEntries={["/endpoints/e1"]}>
        <QueryClientProvider client={qc}>
          <Routes>
            <Route path="/endpoints/:id" element={<EndpointDetail />} />
          </Routes>
        </QueryClientProvider>
      </MemoryRouter>,
    );
    await screen.findByText("Endpoint e1");
    expect(screen.getByText("https://a.example")).toBeInTheDocument();
    expect(screen.getByText("desc")).toBeInTheDocument();
  });

  it("clears the secret from the DOM when the rotate reveal modal is closed", async () => {
    server.use(
      http.get("/v1/endpoints/e1", () =>
        HttpResponse.json({
          id: "e1",
          url: "https://a.example",
          description: "desc",
          created_at: "2025-01-01T00:00:00Z",
        }),
      ),
      http.post("/v1/endpoints/e1/rotate-secret", () =>
        HttpResponse.json({ secret: "whsec_rotated" }, { status: 200 }),
      ),
    );
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(
      <MemoryRouter initialEntries={["/endpoints/e1"]}>
        <QueryClientProvider client={qc}>
          <Routes>
            <Route path="/endpoints/:id" element={<EndpointDetail />} />
          </Routes>
        </QueryClientProvider>
      </MemoryRouter>,
    );
    await screen.findByText("Endpoint e1");

    // Click rotate secret
    fireEvent.click(screen.getByRole("button", { name: /rotate secret/i }));

    // Secret should appear in the reveal modal
    await screen.findByText("whsec_rotated");
    expect(screen.getByText(/will not be shown again/i)).toBeInTheDocument();

    // Click "I saved it" to close
    fireEvent.click(screen.getByRole("button", { name: /i saved it/i }));

    // Secret should be gone from DOM — the mutation result was cleared
    await waitFor(() => expect(screen.queryByText("whsec_rotated")).not.toBeInTheDocument());
  });
});
