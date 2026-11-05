import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../web",
    assetsDir: "",
    emptyOutDir: true,
    sourcemap: false,
    rollupOptions: {
      output: {
        manualChunks: {
          ui: [
            "react",
            "react-dom",
            "@mui/material",
            "@emotion/react",
            "@emotion/styled",
          ],
        },
      },
    },
  },
});
