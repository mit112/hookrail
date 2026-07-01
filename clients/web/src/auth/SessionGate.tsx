import type { ReactNode } from "react";
import { useEffect, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { fetchSession, fetchPublicConfig, demoLogin } from "./session";
import { Login } from "./Login";
import { RoleProvider } from "./role";

// DemoBanner explains the read-only public demo so a first-time visitor
// understands what they are looking at.
function DemoBanner() {
  return (
    <div className="demo-banner" role="status">
      <strong>Live read-only demo.</strong> You are signed in as a viewer — events
      are being delivered continuously in the background. Browse deliveries,
      endpoints, and the dead-letter queue; write actions are disabled.{" "}
      <a href="https://github.com/mit112/hookrail" target="_blank" rel="noreferrer">
        See the code →
      </a>
    </div>
  );
}

export function SessionGate({ children }: { children: ReactNode }) {
  const qc = useQueryClient();
  const { data: pub } = useQuery({
    queryKey: ["public-config"],
    queryFn: fetchPublicConfig,
    retry: false,
    staleTime: Infinity,
  });
  const { data, isLoading, isError } = useQuery({
    queryKey: ["session"],
    queryFn: fetchSession,
    retry: false,
    staleTime: 60_000,
  });

  // In demo mode, transparently obtain a read-only session so a visitor never
  // hits a login wall. Attempt once to avoid a loop if demo-login ever fails.
  const attempted = useRef(false);
  const unauthenticated = !isLoading && (isError || !data?.authenticated);
  useEffect(() => {
    if (pub?.demo && unauthenticated && !attempted.current) {
      attempted.current = true;
      demoLogin()
        .then(() => qc.invalidateQueries({ queryKey: ["session"] }))
        .catch(() => { /* fall through to the login form */ });
    }
  }, [pub?.demo, unauthenticated, qc]);

  if (isLoading) return <p>Loading…</p>;
  if (unauthenticated) {
    // Demo mode: show a brief interstitial while auto-login completes rather
    // than flashing the login form. Only fall back to Login if it truly failed.
    if (pub?.demo && !attempted.current) return <p>Starting demo…</p>;
    if (pub?.demo && attempted.current && !isError) return <p>Starting demo…</p>;
    return <Login onSuccess={() => qc.invalidateQueries({ queryKey: ["session"] })} />;
  }
  if (!data) return <p>Loading…</p>;
  return (
    <RoleProvider role={data.role}>
      {pub?.demo && <DemoBanner />}
      {children}
    </RoleProvider>
  );
}
