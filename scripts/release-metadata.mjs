import { appendFile, readFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { validateReleaseTag } from './release.mjs';

const image = 'ghcr.io/mapherez/nox-backend';

export function releaseMetadata(tag, contents, revision, created = new Date()) {
  const version = validateReleaseTag(tag, contents);
  // A validated SemVer can contain '-' only when it has a prerelease component.
  const prerelease = version.includes('-');
  if (!/^[0-9a-f]{40}$/.test(revision)) throw new Error('A revisao OCI deve ser o commit SHA completo.');
  return {
    prerelease,
    tags: [`${image}:${tag}`, ...(!prerelease ? [`${image}:latest`] : [])].join('\n'),
    labels: [
      `org.opencontainers.image.version=${tag}`,
      `org.opencontainers.image.revision=${revision}`,
      'org.opencontainers.image.source=https://github.com/mapherez/nox-backend',
      `org.opencontainers.image.created=${created.toISOString()}`,
    ].join('\n'),
  };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  (async () => {
    if (process.argv.length !== 3) throw new Error('Uso: node scripts/release-metadata.mjs vX.Y.Z');
    const metadata = releaseMetadata(process.argv[2],
      await readFile(new URL('../VERSION', import.meta.url), 'utf8'), process.env.RELEASE_REVISION);
    if (process.env.GITHUB_OUTPUT) {
      const delimiter = `metadata_${randomUUID()}`;
      await appendFile(process.env.GITHUB_OUTPUT, Object.entries(metadata)
        .map(([key, value]) => `${key}<<${delimiter}\n${value}\n${delimiter}\n`).join(''));
    } else {
      console.log(JSON.stringify(metadata, null, 2));
    }
  })().catch(error => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
