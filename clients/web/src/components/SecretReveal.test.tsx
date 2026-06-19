import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { describe, it, expect, vi, afterEach } from "vitest";
import { SecretReveal } from "./SecretReveal";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("SecretReveal", () => {
  it("renders the secret, warning, and copy button", () => {
    render(<SecretReveal secret="whsec_x" onClose={vi.fn()} />);
    expect(screen.getByText("whsec_x")).toBeInTheDocument();
    expect(screen.getByText(/will not be shown again/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /copy/i })).toBeInTheDocument();
  });

  it("calls onClose when 'I saved it' is clicked", () => {
    const onClose = vi.fn();
    render(<SecretReveal secret="whsec_x" onClose={onClose} />);
    fireEvent.click(screen.getByRole("button", { name: /i saved it/i }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("does NOT write to localStorage or sessionStorage", () => {
    const lsSetItem = vi.spyOn(Storage.prototype, "setItem");
    const ssSetItem = vi.spyOn(Storage.prototype, "setItem");
    render(<SecretReveal secret="whsec_x" onClose={vi.fn()} />);
    expect(lsSetItem).not.toHaveBeenCalled();
    expect(ssSetItem).not.toHaveBeenCalled();
  });

  it("copies secret to clipboard on copy button click", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });
    render(<SecretReveal secret="whsec_x" onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: /copy/i }));
    expect(writeText).toHaveBeenCalledWith("whsec_x");
  });
});
