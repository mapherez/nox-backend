import { execFileSync } from 'node:child_process';
import { readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const semver = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;

export function normalizeVersion(input) {
  const version = typeof input === 'string' ? input.replace(/^v/, '') : '';
  // JS $ also matches before a final newline; reject any whitespace explicitly.
  if (!semver.test(version) || /\s/.test(version)) {
    throw new Error('Versao invalida. Usa X.Y.Z ou X.Y.Z-prerelease.');
  }
  if (version.includes('+')) {
    throw new Error('Metadados de build (+) nao sao permitidos nas tags Docker. Usa X.Y.Z ou X.Y.Z-prerelease.');
  }
  if (`v${version}`.length > 128) throw new Error('A tag Docker excede 128 caracteres.');
  return version;
}

export function parseVersionFile(contents) {
  const version = contents.replace(/\r?\n$/, '');
  if (version.startsWith('v') || normalizeVersion(version) !== version) {
    throw new Error('VERSION deve conter apenas a versao SemVer sem prefixo v.');
  }
  return version;
}

export function validateReleaseTag(tag, contents) {
  if (typeof tag !== 'string' || !tag.startsWith('v')) {
    throw new Error('A tag de release deve ser SemVer com prefixo v.');
  }
  const version = normalizeVersion(tag);
  if (`v${version}` !== tag || parseVersionFile(contents) !== version) {
    throw new Error(`VERSION nao corresponde exactamente a ${tag}.`);
  }
  return version;
}

export function runReleaseChecks(root, tag, run = execFileSync) {
  run('go', ['test', './...'], { cwd: resolve(root, 'backend'), stdio: 'inherit' });
  run('docker', ['build', '--build-arg', `VERSION=${tag}`, '-t', 'nox-backend:release-check', './backend'],
    { cwd: root, stdio: 'inherit' });
}

export async function release(root, input, { check = runReleaseChecks } = {}) {
  const version = normalizeVersion(input);
  const tag = `v${version}`;
  const git = (...args) => execFileSync('git', args, {
    cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
  if (resolve(git('rev-parse', '--show-toplevel')) !== resolve(root)) {
    throw new Error('Executa o script na root do repositorio nox-backend.');
  }
  if (git('status', '--porcelain', '--untracked-files=all')) {
    throw new Error('A arvore Git tem alteracoes. Faz commit ou stash antes da release.');
  }
  const branch = git('symbolic-ref', '--quiet', '--short', 'HEAD');
  const path = resolve(root, 'VERSION');
  const original = await readFile(path, 'utf8');
  if (parseVersionFile(original) === version) throw new Error('VERSION ja usa essa versao; escolhe uma nova versao.');
  if (git('tag', '--list', tag)) throw new Error(`A tag ${tag} ja existe localmente.`);
  // A failed remote lookup must abort too; never assume an unreachable origin has no tag.
  if (git('ls-remote', '--tags', 'origin', `refs/tags/${tag}`, `refs/tags/${tag}^{}`)) {
    throw new Error(`A tag ${tag} ja existe em origin.`);
  }
  git('var', 'GIT_AUTHOR_IDENT');
  git('var', 'GIT_COMMITTER_IDENT');
  const head = git('rev-parse', 'HEAD');
  try {
    await writeFile(path, `${version}${original.endsWith('\r\n') ? '\r\n' : '\n'}`);
    await check(root, tag);
    validateReleaseTag(tag, await readFile(path, 'utf8'));
    if (git('rev-parse', 'HEAD') !== head || git('symbolic-ref', '--quiet', '--short', 'HEAD') !== branch ||
        git('diff', '--name-only').split('\n').filter(Boolean).some(file => file !== 'VERSION') ||
        git('diff', '--cached', '--name-only') || git('ls-files', '--others', '--exclude-standard')) {
      throw new Error('A validacao alterou o repositorio fora de VERSION. Reve as alteracoes antes de publicar.');
    }
    git('add', '--', 'VERSION');
    git('commit', '-m', `chore: release ${tag}`);
  } catch (error) {
    // Restore our edit only if a release commit was not created. Keep unrelated changes.
    if (git('rev-parse', 'HEAD') === head) {
      await writeFile(path, original);
      git('restore', '--staged', '--', 'VERSION');
    }
    throw error;
  }
  const pushArgs = ['push', '--atomic', 'origin', `HEAD:refs/heads/${branch}`, `refs/tags/${tag}:refs/tags/${tag}`];
  const quote = arg => `'${arg.replaceAll("'", process.platform === 'win32' ? "''" : "'\\''")}'`;
  const recovery = `git push --atomic origin ${pushArgs.slice(3).map(quote).join(' ')}`;
  try {
    git('tag', '-a', tag, '-m', `Release ${tag}`);
  } catch (error) {
    throw new Error(`Commit de release mantido localmente. A tag falhou. Recovery: git tag -a ${tag} -m "Release ${tag}"; depois ${recovery}`, { cause: error });
  }
  try {
    git(...pushArgs);
  } catch (error) {
    throw new Error(`Push falhou. O commit e a tag ${tag} ficaram locais. Corrige a causa e repete: ${recovery}`, { cause: error });
  }
  return tag;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  (async () => {
    if (process.argv.length !== 3) throw new Error('Uso: npm run release -- X.Y.Z');
    const tag = await release(repoRoot, process.argv[2]);
    console.log(`Commit e tag ${tag} publicados. O GitHub Actions vai publicar a imagem e criar a release.`);
    console.log(`https://github.com/mapherez/nox-backend/actions/workflows/release.yml`);
  })().catch(error => {
    console.error(error.stderr?.toString().trim() || error.message);
    if (error.cause) console.error(error.cause.stderr?.toString().trim() || error.cause.message);
    process.exitCode = 1;
  });
}
