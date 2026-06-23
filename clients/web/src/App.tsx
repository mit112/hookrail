import type { ReactNode } from "react";
import { Routes, Route, Navigate } from "react-router-dom";
import { SessionGate } from "./auth/SessionGate";
import { useRole, roleAtLeast, type Role } from "./auth/role";
import { Endpoints } from "./routes/Endpoints";
import { EndpointDetail } from "./routes/EndpointDetail";
import { EndpointNew, EndpointEdit } from "./routes/EndpointForm";
import { Subscriptions } from "./routes/Subscriptions";
import { SubscriptionDetail } from "./routes/SubscriptionDetail";
import { SubscriptionNew, SubscriptionEdit } from "./routes/SubscriptionForm";
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

export function App() {
  return (
    <SessionGate>
      <Routes>
        <Route path="/" element={<Navigate to="/endpoints" replace />} />
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
    </SessionGate>
  );
}
