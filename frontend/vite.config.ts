import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  test: {
    environment: "jsdom",
    // The contrast test reads the stylesheet itself, which Vitest leaves empty unless it processes CSS.
    css: true,
    setupFiles: ["./src/test/setup.ts"],
  },
});
