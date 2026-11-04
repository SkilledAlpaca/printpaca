import { svelte } from "@sveltejs/vite-plugin-svelte";
import { defineConfig } from "vitest/config";

export default defineConfig({
  // Compiles runes-in-a-module files (*.svelte.ts) the way vite does; plain TS leaves `$state(...)` as a call
  // to a nonexistent global.
  plugins: [svelte()],
  test: {
    include: ["web/src/**/*.test.ts", "scripts/**/*.test.ts"],
  },
});
