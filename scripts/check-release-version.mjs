import { readFile } from 'node:fs/promises';
import { validatePackageVersion, validateReleaseTag } from './release.mjs';

try {
  if (process.argv.length !== 3) throw new Error('Uso: node scripts/check-release-version.mjs vX.Y.Z');
  const version = validateReleaseTag(process.argv[2], await readFile(new URL('../VERSION', import.meta.url), 'utf8'));
  validatePackageVersion(await readFile(new URL('../package.json', import.meta.url), 'utf8'), version);
  console.log(`VERSION e package.json correspondem a ${process.argv[2]}.`);
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
