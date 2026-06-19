import { Routes, Route, Navigate } from "react-router-dom";
import { SessionGate } from "./auth/SessionGate";
import { Endpoints } from "./routes/Endpoints";
import { EndpointDetail } from "./routes/EndpointDetail";
import { Subscriptions } from "./routes/Subscriptions";
import { SubscriptionDetail } from "./routes/SubscriptionDetail";

export function App() {
  return (
    <SessionGate>
      <Routes>
        <Route path="/" element={<Navigate to="/endpoints" replace />} />
        <Route path="/endpoints" element={<Endpoints />} />
        <Route path="/endpoints/:id" element={<EndpointDetail />} />
        <Route path="/subscriptions" element={<Subscriptions />} />
        <Route path="/subscriptions/:id" element={<SubscriptionDetail />} />
      </Routes>
    </SessionGate>
  );
}
