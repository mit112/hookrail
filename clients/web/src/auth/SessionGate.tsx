import type { ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { fetchSession } from "./session";
import { Login } from "./Login";

export function SessionGate({ children }: { children: ReactNode }) {
  const qc = useQueryClient();
  const { data, isLoading, isError } = useQuery({
    queryKey: ["session"],
    queryFn: fetchSession,
    retry: false,
    staleTime: 60_000,
  });

  if (isLoading) return <p>Loading…</p>;
  if (isError || !data) return <Login onSuccess={() => qc.invalidateQueries({ queryKey: ["session"] })} />;
  return <>{children}</>;
}
