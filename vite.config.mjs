import { defineConfig } from "vite-plus";
import formatOptions from "./frontend/format-options.json" with { type: "json" };

export default defineConfig({ fmt: formatOptions });
