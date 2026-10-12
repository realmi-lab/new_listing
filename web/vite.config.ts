import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In development the Go program serves the API on 8770.
export default defineConfig({
  plugins: [react()],
  server: { proxy: { "/api": "http://127.0.0.1:8770" } },
  build: { outDir: "dist", emptyOutDir: true, chunkSizeWarningLimit: 600 },
});
