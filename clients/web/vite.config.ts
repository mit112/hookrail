/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/v1": "http://localhost:8085",
      "/api": "http://localhost:8085",
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./vitest.setup.ts"],
    globals: false,
    exclude: ["node_modules", "dist-ts"],
    pool: "forks",
  },
});
