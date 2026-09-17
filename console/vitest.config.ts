import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import path from "node:path";

// Console-local vitest config. The root vitest.config.ts pins
// pool: cloudflarePool for backend tests against workerd; React/DOM
// tests can't run there. This config keeps console tests on jsdom and
// the root config now excludes apps/console/** so root `vitest run`
// won't try to load these.
//
// Run with: pnpm --filter managed-agents-console test
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.{test,spec}.{ts,tsx}"],
    globals: false,
    css: false,
    // Exclude E2E tests from the default setup since they need real network access
    // E2E tests should be run separately with: pnpm test -- deepseek-*-e2e.test.ts
    // and handle MSW disabling internally
  },
});
