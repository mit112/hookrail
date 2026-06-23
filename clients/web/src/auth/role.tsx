import { createContext, useContext, type ReactNode } from "react";

export type Role = "viewer" | "operator" | "admin";

const order: Record<Role, number> = { viewer: 0, operator: 1, admin: 2 };

/** roleAtLeast reports whether `have` meets the `min` privilege level. */
export function roleAtLeast(have: Role, min: Role): boolean {
  return order[have] >= order[min];
}

// Default to the least privilege so a component rendered without a provider
// never reveals privileged controls. In production SessionGate always provides
// the authenticated role. UI gating is cosmetic; the server is the boundary.
const RoleContext = createContext<Role>("viewer");

export function RoleProvider({ role, children }: { role: Role; children: ReactNode }) {
  return <RoleContext.Provider value={role}>{children}</RoleContext.Provider>;
}

export function useRole(): Role {
  return useContext(RoleContext);
}

/** RequireRole renders children only when the live role meets `min`. */
export function RequireRole({ min, children }: { min: Role; children: ReactNode }) {
  return roleAtLeast(useRole(), min) ? <>{children}</> : null;
}
