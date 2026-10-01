#!/usr/bin/env node

import { readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { createInterface } from "node:readline/promises";
import { fileURLToPath } from "node:url";

const rootDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const files = [
  "plugin/package.json",
  "plugin/package-lock.json",
  "plugin/manifest.json",
  "manifest.json",
  "plugin/versions.json",
  "versions.json",
];

try {
  await main();
} catch (error) {
  console.error(`Failed to update NoX Sync version: ${error.message}`);
  process.exitCode = 1;
}

async function main() {
  const args = process.argv.slice(2);
  if (args.length === 1 && ["--help", "-h"].includes(args[0])) {
    console.log(`Usage:
  npm run version:set
  npm run version:set -- <version>

Updates the NoX Sync plugin version in:
${files.map((file) => `  ${file}`).join("\n")}

Use MAJOR.MINOR.PATCH (an optional v prefix is accepted).
Preserves previous versions.json entries and the minimum Obsidian version.
Run npm run build afterwards to regenerate plugin/dist.
`);
    return;
  }
  if (args.length > 1 || args[0]?.startsWith("-")) {
    throw new Error("Expected a single version argument. See --help.");
  }

  // Read and validate every source before modifying any file.
  const originals = await Promise.all(
    files.map((file) => readFile(path.join(rootDir, file), "utf8")),
  );
  const documents = originals.map((text, index) => {
    const value = JSON.parse(text);
    if (value === null || typeof value !== "object" || Array.isArray(value)) {
      throw new Error(`Expected a JSON object in ${files[index]}.`);
    }
    return value;
  });
  const [packageJson, packageLock, pluginManifest, rootManifest, pluginVersions, rootVersions] = documents;
  for (const [label, version] of [
    [files[0], packageJson.version],
    [files[1], packageLock.version],
    [`${files[1]} root package`, packageLock.packages?.[""]?.version],
    [files[2], pluginManifest.version],
    [files[3], rootManifest.version],
    [`${files[2]} minAppVersion`, pluginManifest.minAppVersion],
    [`${files[3]} minAppVersion`, rootManifest.minAppVersion],
  ]) {
    if (typeof version !== "string" || !isVersion(version)) {
      throw new Error(`Invalid or missing version in ${label}.`);
    }
  }
  if (pluginManifest.minAppVersion !== rootManifest.minAppVersion) {
    throw new Error("The root and plugin manifests must have the same minAppVersion.");
  }

  const currentVersion = packageJson.version;
  console.log(`Current NoX Sync version: ${currentVersion}`);
  let requestedVersion = args[0];
  if (requestedVersion === undefined) {
    if (!process.stdin.isTTY) {
      throw new Error("No version provided. Run interactively or pass it after --.");
    }
    const prompt = createInterface({ input: process.stdin, output: process.stdout });
    try {
      requestedVersion = await prompt.question("New version (Enter to cancel): ");
    } finally {
      prompt.close();
    }
    if (requestedVersion.trim() === "") {
      console.log("Version update cancelled.");
      return;
    }
  }

  const nextVersion = requestedVersion.trim().replace(/^v/i, "");
  if (!isVersion(nextVersion)) {
    throw new Error(`Invalid version: ${requestedVersion}. Use MAJOR.MINOR.PATCH, e.g. 1.0.2.`);
  }
  packageJson.version = nextVersion;
  packageLock.version = nextVersion;
  packageLock.packages[""].version = nextVersion;
  pluginManifest.version = nextVersion;
  rootManifest.version = nextVersion;
  pluginVersions[nextVersion] = pluginManifest.minAppVersion;
  rootVersions[nextVersion] = pluginManifest.minAppVersion;

  const updated = documents.map((value, index) => {
    const newline = originals[index].includes("\r\n") ? "\r\n" : "\n";
    const text = `${JSON.stringify(value, null, 2)}\n`;
    // Avoid rewriting files whose parsed content already matches (including formatting).
    return JSON.stringify(JSON.parse(originals[index])) === JSON.stringify(value)
      ? originals[index]
      : text.replaceAll("\n", newline);
  });
  const changed = files.map((_, index) => index).filter((index) => updated[index] !== originals[index]);
  if (changed.length === 0) {
    console.log(`NoX Sync is already at version ${nextVersion}.`);
    return;
  }

  const written = [];
  try {
    for (const index of changed) {
      written.push(index);
      await writeFile(path.join(rootDir, files[index]), updated[index], "utf8");
    }
  } catch (error) {
    const restored = await Promise.allSettled(
      written.map((index) => writeFile(path.join(rootDir, files[index]), originals[index], "utf8")),
    );
    const failures = restored.flatMap((result, index) =>
      result.status === "rejected" ? [files[written[index]]] : [],
    );
    if (failures.length > 0) {
      throw new Error(`${error.message}. Could not restore: ${failures.join(", ")}.`);
    }
    throw error;
  }

  console.log(`Updated NoX Sync from ${currentVersion} to ${nextVersion}:`);
  for (const index of changed) {
    console.log(`- ${files[index]}`);
  }
  console.log("Run npm run build to regenerate plugin/dist before releasing.");
}

function isVersion(value) {
  return /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(value);
}
