// Hookrail ingest profile (§11): 1KB payload, healthy consumers.
// Fan-out is a property of the seeded subscriptions, not this script.
// ONE invocation = ONE phase: run.sh invokes it twice (warm-up, sustained)
// and opens the measurement window between them on PG's clock — a single
// two-scenario run would let k6 startup delay leak warm-up traffic into the
// sustained window.
// Env: API_URL, PRODUCER_KEY, RATE (events/s), DURATION (e.g. "10m")
import http from "k6/http";
import { check } from "k6";

const payload = JSON.stringify({
  topic: "load.k6",
  payload: { blob: "x".repeat(900), seq: 0 }, // ~1KB body total
});

export const options = {
  scenarios: {
    phase: {
      executor: "constant-arrival-rate",
      rate: Number(__ENV.RATE || 200),
      timeUnit: "1s",
      duration: __ENV.DURATION || "10m",
      preAllocatedVUs: 400,
    },
  },
  thresholds: {
    http_req_failed: ["rate<0.001"],
  },
};

export default function () {
  const res = http.post(`${__ENV.API_URL}/v1/events`, payload, {
    headers: {
      Authorization: `Bearer ${__ENV.PRODUCER_KEY}`,
      "Content-Type": "application/json",
    },
  });
  check(res, { "202": (r) => r.status === 202 });
}
