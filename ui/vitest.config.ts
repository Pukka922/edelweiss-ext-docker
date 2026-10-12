import solid from "vite-plugin-solid";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [solid()],
  test: { environment: "jsdom", setupFiles: ["./src/test-setup.ts"] },
});
