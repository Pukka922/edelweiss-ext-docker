import { fileURLToPath } from "node:url";
import solid from "vite-plugin-solid";
import { defineConfig } from "vitest/config";

// The icon package's "solid" export condition points at about 6000 JSX source modules, which vite-plugin-solid would
// transform again for every test file that imports it (the full run took three times as long). Tests use its
// precompiled ESM bundle instead; the production build (vite.config.ts) is not affected.
const tablerIcons = fileURLToPath(
  new URL("./node_modules/@tabler/icons-solidjs/dist/esm/tabler-icons-solidjs.js", import.meta.url),
);

export default defineConfig({
  plugins: [solid()],
  resolve: { alias: { "@tabler/icons-solidjs": tablerIcons } },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test-setup.ts"],
    // one Solid runtime (the one of the tests) for the icons too, never a second, server-side copy loaded by Node
    server: { deps: { inline: [/@tabler\/icons-solidjs/] } },
  },
});
