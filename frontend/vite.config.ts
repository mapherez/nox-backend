import { defineConfig } from "vite-plus";
import react from "@vitejs/plugin-react";

const backend = process.env.NOX_FRONTEND_BACKEND_URL || "http://localhost:8080";

export default defineConfig({
  staged: { "*": "vp check --fix" },
  plugins: [react()],
  base: "/vault-dashboard/",
  server: {
    proxy: {
      "/api": backend,
      "/auth": backend,
      "/v1": backend,
      "/mcp": backend,
      "/nox": backend,
      "/vault-dashboard/users": backend,
      "/vault-dashboard/vaults": backend,
      "/vault-dashboard/api-key": backend,
    },
  },
  build: {
    rolldownOptions: {
      output: {
        entryFileNames: "assets/dashboard.js",
        assetFileNames: (asset) =>
          asset.names.some((name) => name.endsWith(".css"))
            ? "assets/dashboard[extname]"
            : "assets/[name]-[hash][extname]",
      },
    },
  },
  test: {
    environment: "jsdom",
    environmentOptions: { jsdom: { url: "https://backend.example.com" } },
    include: ["src/**/*.test.tsx"],
    clearMocks: true,
  },
  lint: {
    options: { typeAware: true, typeCheck: true },
    ignorePatterns: [
      "dist/**",
      "playwright-report/**",
      "test-results/**",
      "**/node_modules/**",
      "**/dist/**",
      "**/build/**",
      "**/target/**",
      "**/vendor/**",
      "**/*.md",
      "**/*.mdx",
      "**/package-lock.json",
      "**/pnpm-lock.yaml",
    ],
  },
  fmt: {
    insertFinalNewline: true,
    endOfLine: "lf",
    useTabs: false,
    tabWidth: 2,
    sortPackageJson: false,
    ignorePatterns: [
      "dist/**",
      "playwright-report/**",
      "test-results/**",
      "**/node_modules/**",
      "**/dist/**",
      "**/build/**",
      "**/target/**",
      "**/vendor/**",
      "**/*.md",
      "**/*.mdx",
      "**/package-lock.json",
      "**/pnpm-lock.yaml",
    ],
  },
});
