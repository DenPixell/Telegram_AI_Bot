import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev-сервер на 3000, как PUBLIC_URL: тогда Origin совпадает и CSRF-проверка проходит.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 3000,
    host: true,
    proxy: { "/api": { target: process.env.API_URL ?? "http://localhost:8080" } },
  },
  build: { target: "es2022", sourcemap: false },
});
