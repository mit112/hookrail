import { useMutation, useQueryClient } from "@tanstack/react-query";
import { request } from "../api/client";

export function useReplay() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      request("POST", `/v1/dlq/${encodeURIComponent(id)}/replay`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["dlq"] });
      qc.invalidateQueries({ queryKey: ["deliveries"] });
    },
    retry: false,
  });
}
