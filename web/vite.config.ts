import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The build lands where the Coordinator embeds it (internal/panel).
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../internal/panel/dist/app",
    emptyOutDir: true,
    target: "es2020",
  },
  server: { proxy: { "/v1": "http://127.0.0.1:8080" } },
  test: { include: ["src/**/*.test.ts"] },
});
