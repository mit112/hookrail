import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { Login } from "./Login";
import * as session from "./session";

describe("Login", () => {
  beforeEach(() => vi.restoreAllMocks());
  it("calls login then onSuccess", async () => {
    const spy = vi.spyOn(session, "login").mockResolvedValue();
    const onSuccess = vi.fn();
    render(<Login onSuccess={onSuccess} />);
    fireEvent.change(screen.getByLabelText(/username/i), { target: { value: "alice" } });
    fireEvent.change(screen.getByLabelText(/password/i), { target: { value: "pw" } });
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));
    await waitFor(() => expect(spy).toHaveBeenCalledWith("alice", "pw"));
    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
  });
});
