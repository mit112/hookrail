import { useMutation, useQueryClient } from "@tanstack/react-query";
import { request } from "../api/client";

export function useTestEvent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { topic: string; payload: unknown }) =>
      request("POST", "/api/test-event", { body }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["deliveries"] });
    },
    retry: false,
  });
}
