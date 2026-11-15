import { copyFileSync, mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { defineConfig, loadEnv } from "vite";
import preact from "@preact/preset-vite";

export default defineConfig(({ mode, command }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const apiTarget = env.CSGHUB_LITE_API_TARGET || env.VITE_API_TARGET || "http://localhost:11435";
  const distDir = resolve(__dirname, "dist");
  const openapiSource = resolve(__dirname, "../openapi/local-api.json");
  const copyOpenAPISpecPlugin = {
    name: "copy-openapi-spec",
    closeBundle() {
      const target = resolve(distDir, "openapi/local-api.json");
      mkdirSync(dirname(target), { recursive: true });
      copyFileSync(openapiSource, target);
    },
  };

  return {
    plugins: command === "build" ? [preact(), copyOpenAPISpecPlugin] : [preact()],
    server: {
      port: 5173,
      proxy: {
        "/api": {
          target: apiTarget,
          changeOrigin: true,
        },
        "/v1": {
          target: apiTarget,
          changeOrigin: true,
        },
      },
    },
    build: {
      outDir: "dist",
      emptyOutDir: true,
      rollupOptions: {
        input: {
          main: resolve(__dirname, "index.html"),
          login: resolve(__dirname, "login.html"),
          apiDocs: resolve(__dirname, "api-docs.html"),
        },
      },
    },
  };
});
