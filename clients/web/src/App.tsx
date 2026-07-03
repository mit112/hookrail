import type { ReactNode } from "react";
import { Routes, Route, Navigate, NavLink } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { SessionGate } from "./auth/SessionGate";
import { fetchSession, logout } from "./auth/session";
import { useRole, roleAtLeast, RequireRole, type Role } from "./auth/role";
import { Endpoints } from "./routes/Endpoints";
import { EndpointDetail } from "./routes/EndpointDetail";
import { EndpointNew, EndpointEdit } from "./routes/EndpointForm";
import { Subscriptions } from "./routes/Subscriptions";
import { SubscriptionDetail } from "./routes/SubscriptionDetail";
import { SubscriptionNew, SubscriptionEdit } from "./routes/SubscriptionForm";
import { Overview } from "./routes/Overview";
import { Deliveries } from "./routes/Deliveries";
import { DeliveryTimeline } from "./routes/DeliveryTimeline";
import { DLQ } from "./routes/DLQ";
import { TestEvent } from "./routes/TestEvent";

// RoleRoute redirects to a safe read page when the live role is below min, so a
// privileged page is not reachable by direct URL for a lower role (UX only; the
// server is the enforcement boundary).
function RoleRoute({ min, children }: { min: Role; children: ReactNode }) {
  return roleAtLeast(useRole(), min) ? <>{children}</> : <Navigate to="/endpoints" replace />;
}

const navClass = ({ isActive }: { isActive: boolean }) =>
  "nav-item" + (isActive ? " is-active" : "");

function AppShell({ children }: { children: ReactNode }) {
  const role = useRole();
  const qc = useQueryClient();
  const { data: session } = useQuery({ queryKey: ["session"], queryFn: fetchSession });
  const signOut = async () => {
    try {
      await logout();
    } finally {
      qc.invalidateQueries({ queryKey: ["session"] });
    }
  };
  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <span className="brand-mark" aria-hidden="true" />
          <span className="brand-name">Hookrail</span>
          <span className="brand-sub">delivery plane</span>
        </div>
        <div className="topbar-right">
          {session?.username && <span className="who">{session.username}</span>}
          <span className={`role-badge role-badge--${role}`}>{role}</span>
          <button className="btn btn--ghost" onClick={signOut}>Sign out</button>
        </div>
      </header>
      <div className="app-body">
        <nav className="sidebar" aria-label="Primary">
          <NavLink to="/overview" className={navClass}>Overview</NavLink>
          <NavLink to="/endpoints" className={navClass}>Endpoints</NavLink>
          <NavLink to="/subscriptions" className={navClass}>Subscriptions</NavLink>
          <NavLink to="/deliveries" className={navClass}>Deliveries</NavLink>
          <NavLink to="/dlq" className={navClass}>Dead letter</NavLink>
          <RequireRole min="operator">
            <NavLink to="/test-event" className={navClass}>Test event</NavLink>
          </RequireRole>
        </nav>
        <main className="app-main">{children}</main>
      </div>
    </div>
  );
}

export function App() {
  return (
    <SessionGate>
      <AppShell>
      <Routes>
        <Route path="/" element={<Navigate to="/overview" replace />} />
        <Route path="/overview" element={<Overview />} />
        <Route path="/endpoints" element={<Endpoints />} />
        <Route path="/endpoints/new" element={<RoleRoute min="admin"><EndpointNew /></RoleRoute>} />
        <Route path="/endpoints/:id" element={<EndpointDetail />} />
        <Route path="/endpoints/:id/edit" element={<RoleRoute min="admin"><EndpointEdit /></RoleRoute>} />
        <Route path="/subscriptions" element={<Subscriptions />} />
        <Route path="/subscriptions/new" element={<RoleRoute min="admin"><SubscriptionNew /></RoleRoute>} />
        <Route path="/subscriptions/:id" element={<SubscriptionDetail />} />
        <Route path="/subscriptions/:id/edit" element={<RoleRoute min="admin"><SubscriptionEdit /></RoleRoute>} />
        <Route path="/deliveries" element={<Deliveries />} />
        <Route path="/deliveries/:id" element={<DeliveryTimeline />} />
        <Route path="/dlq" element={<DLQ />} />
        <Route path="/test-event" element={<RoleRoute min="operator"><TestEvent /></RoleRoute>} />
      </Routes>
      </AppShell>
    </SessionGate>
  );
}
