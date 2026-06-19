import { Routes, Route, Navigate } from "react-router-dom";
import { SessionGate } from "./auth/SessionGate";
import { Endpoints } from "./routes/Endpoints";
import { EndpointDetail } from "./routes/EndpointDetail";
import { EndpointNew, EndpointEdit } from "./routes/EndpointForm";
import { Subscriptions } from "./routes/Subscriptions";
import { SubscriptionDetail } from "./routes/SubscriptionDetail";
import { SubscriptionNew, SubscriptionEdit } from "./routes/SubscriptionForm";
import { Deliveries } from "./routes/Deliveries";
import { DeliveryTimeline } from "./routes/DeliveryTimeline";
import { DLQ } from "./routes/DLQ";

export function App() {
  return (
    <SessionGate>
      <Routes>
        <Route path="/" element={<Navigate to="/endpoints" replace />} />
        <Route path="/endpoints" element={<Endpoints />} />
        <Route path="/endpoints/new" element={<EndpointNew />} />
        <Route path="/endpoints/:id" element={<EndpointDetail />} />
        <Route path="/endpoints/:id/edit" element={<EndpointEdit />} />
        <Route path="/subscriptions" element={<Subscriptions />} />
        <Route path="/subscriptions/new" element={<SubscriptionNew />} />
        <Route path="/subscriptions/:id" element={<SubscriptionDetail />} />
        <Route path="/subscriptions/:id/edit" element={<SubscriptionEdit />} />
        <Route path="/deliveries" element={<Deliveries />} />
        <Route path="/deliveries/:id" element={<DeliveryTimeline />} />
        <Route path="/dlq" element={<DLQ />} />
      </Routes>
    </SessionGate>
  );
}
