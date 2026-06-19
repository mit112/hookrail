import { useMutation, useQueryClient } from "@tanstack/react-query";
import { z } from "zod";
import { request } from "../api/client";

const RotateResponse = z.object({ secret: z.string() });

export function useRotateSecret() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      request("POST", `/v1/endpoints/${encodeURIComponent(id)}/rotate-secret`, {
        schema: RotateResponse,
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["endpoints"] }),
    retry: false,
  });
}
