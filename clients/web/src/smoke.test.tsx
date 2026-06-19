import { render, screen } from "@testing-library/react";
import { describe, it, expect, beforeAll, afterAll, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { setupServer } from "msw/node";
import { http, HttpResponse } from "msw";
import { App } from "./App";

const server = setupServer(
  http.get("/api/session", () => HttpResponse.json({})),
);
beforeAll(() => server.listen());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

describe("App", () => {
  it("renders the shell", async () => {
    const qc = new QueryClient();
    render(
      <MemoryRouter>
        <QueryClientProvider client={qc}>
          <App />
        </QueryClientProvider>
      </MemoryRouter>,
    );
    expect(await screen.findByText(/hookrail/i)).toBeInTheDocument();
  });
});
