import { render, screen, cleanup } from "@testing-library/react";
import { describe, it, expect, afterEach } from "vitest";
import { roleAtLeast, RoleProvider, RequireRole } from "./role";

afterEach(cleanup);

describe("roleAtLeast", () => {
  it("orders viewer < operator < admin", () => {
    expect(roleAtLeast("admin", "operator")).toBe(true);
    expect(roleAtLeast("admin", "admin")).toBe(true);
    expect(roleAtLeast("operator", "operator")).toBe(true);
    expect(roleAtLeast("operator", "admin")).toBe(false);
    expect(roleAtLeast("viewer", "operator")).toBe(false);
    expect(roleAtLeast("viewer", "viewer")).toBe(true);
  });
});

describe("RequireRole", () => {
  it("renders children when the role meets the minimum", () => {
    render(
      <RoleProvider role="operator">
        <RequireRole min="operator"><span>gated</span></RequireRole>
      </RoleProvider>,
    );
    expect(screen.getByText("gated")).toBeInTheDocument();
  });
  it("hides children when the role is insufficient", () => {
    render(
      <RoleProvider role="viewer">
        <RequireRole min="admin"><span>gated</span></RequireRole>
      </RoleProvider>,
    );
    expect(screen.queryByText("gated")).not.toBeInTheDocument();
  });
  it("defaults to viewer (least privilege) without a provider", () => {
    render(<RequireRole min="operator"><span>gated</span></RequireRole>);
    expect(screen.queryByText("gated")).not.toBeInTheDocument();
  });
});
