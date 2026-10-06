import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { resolve } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { normalizeVersion, parseVersionFile, release, runReleaseChecks, validateReleaseTag } from './release.mjs';
import { releaseMetadata } from './release-metadata.mjs';

const git = (root, ...args) => execFileSync('git', args, {
  cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
}).trim();

async function repository(t, original = '0.0.0\n') {
  const temporary = await mkdtemp(resolve(tmpdir(), 'nox-backend-release-'));
  t.after(() => rm(temporary, { recursive: true, force: true }));
  const root = resolve(temporary, 'repo');
  const origin = resolve(temporary, 'origin.git');
  await mkdir(root);
  await mkdir(resolve(root, 'backend'));
  await writeFile(resolve(root, 'VERSION'), original);
  await writeFile(resolve(root, 'tracked.txt'), 'original\n');
  git(root, 'init', '--initial-branch=main');
  git(root, 'config', 'user.name', 'Release test');
  git(root, 'config', 'user.email', 'release@example.invalid');
  git(root, 'config', 'commit.gpgsign', 'false');
  git(root, 'config', 'tag.gpgsign', 'false');
  git(root, 'config', 'core.autocrlf', 'false');
  git(root, 'config', 'core.hooksPath', resolve(temporary, 'no-hooks'));
  git(root, 'add', '.');
  git(root, 'commit', '-m', 'Initial fixture');
  git(root, 'init', '--bare', origin);
  git(root, 'remote', 'add', 'origin', origin);
  git(root, 'push', 'origin', 'main');
  return { root, origin, head: git(root, 'rev-parse', 'HEAD'), original };
}

function unchanged({ root, origin, head }, expectedTags = '') {
  assert.equal(git(root, 'rev-parse', 'HEAD'), head);
  assert.equal(git(root, 'tag', '--list'), expectedTags);
  assert.equal(git(origin, 'rev-parse', 'refs/heads/main'), head);
}

const noChecks = { check() {} };

test('invalid versions are rejected before changing Git or VERSION', async t => {
  const fixture = await repository(t);
  for (const version of ['bad', '1.2', '01.2.3', '1.02.3', '1.2.03', '1.2.3-01', '1.2.3-rc..1',
    '1.2.3-', '1.2.3\n', ' 1.2.3', 'vv1.2.3', '', undefined]) {
    await assert.rejects(release(fixture.root, version, noChecks), /Versao invalida/);
  }
  await assert.rejects(release(fixture.root, '1.2.3+build.1', noChecks), /tags Docker/);
  await assert.rejects(release(fixture.root, `1.2.3-${'a'.repeat(128)}`, noChecks), /128/);
  unchanged(fixture);
  assert.equal(await readFile(resolve(fixture.root, 'VERSION'), 'utf8'), fixture.original);
});

test('unchanged version refuses release', async t => {
  const fixture = await repository(t);
  await assert.rejects(release(fixture.root, 'v0.0.0', noChecks), /ja usa essa versao/);
  unchanged(fixture);
});

for (const dirty of ['tracked', 'staged', 'untracked']) {
  test(`dirty tree (${dirty}) refuses release`, async t => {
    const fixture = await repository(t);
    const path = dirty === 'untracked' ? 'untracked.txt' : 'tracked.txt';
    await writeFile(resolve(fixture.root, path), 'keep this\n');
    if (dirty === 'staged') git(fixture.root, 'add', path);
    const status = git(fixture.root, 'status', '--porcelain');
    await assert.rejects(release(fixture.root, '0.1.0', noChecks), /arvore Git/);
    assert.equal(git(fixture.root, 'status', '--porcelain'), status);
    assert.equal(await readFile(resolve(fixture.root, 'VERSION'), 'utf8'), fixture.original);
    unchanged(fixture);
  });
}

test('existing local tag refuses release', async t => {
  const fixture = await repository(t);
  git(fixture.root, 'tag', 'v0.1.0');
  await assert.rejects(release(fixture.root, '0.1.0', noChecks), /existe localmente/);
  unchanged(fixture, 'v0.1.0');
});

test('remote-only annotated tag refuses release', async t => {
  const fixture = await repository(t);
  git(fixture.root, 'tag', '-a', 'v0.1.0', '-m', 'Existing release');
  git(fixture.root, 'push', 'origin', 'refs/tags/v0.1.0');
  git(fixture.root, 'tag', '-d', 'v0.1.0');
  await assert.rejects(release(fixture.root, '0.1.0', noChecks), /existe em origin/);
  unchanged(fixture);
  assert.equal(await readFile(resolve(fixture.root, 'VERSION'), 'utf8'), fixture.original);
});

test('unreachable origin aborts before checks and version update', async t => {
  const fixture = await repository(t);
  git(fixture.root, 'remote', 'set-url', 'origin', resolve(fixture.root, 'missing.git'));
  await assert.rejects(release(fixture.root, '0.1.0', { check() { assert.fail('Must not run checks'); } }));
  unchanged(fixture);
  assert.equal(await readFile(resolve(fixture.root, 'VERSION'), 'utf8'), fixture.original);
});

test('detached HEAD refuses release', async t => {
  const fixture = await repository(t);
  git(fixture.root, 'checkout', '--detach');
  await assert.rejects(release(fixture.root, '0.1.0', noChecks));
  unchanged(fixture);
});

for (const failedCommand of ['go', 'docker']) {
  test(`${failedCommand} check failure restores VERSION byte-for-byte without commit, tag or push`, async t => {
    const fixture = await repository(t, '0.0.0\r\n');
    const calls = [];
    await assert.rejects(release(fixture.root, '0.1.0', {
      async check(root, tag) {
        assert.equal(await readFile(resolve(root, 'VERSION'), 'utf8'), '0.1.0\r\n');
        runReleaseChecks(root, tag, command => {
          calls.push(command);
          if (command === failedCommand) throw new Error(`${command} failed`);
        });
      },
    }), new RegExp(`${failedCommand} failed`));
    assert.deepEqual(calls, failedCommand === 'go' ? ['go'] : ['go', 'docker']);
    assert.equal(await readFile(resolve(fixture.root, 'VERSION'), 'utf8'), fixture.original);
    assert.equal(git(fixture.root, 'status', '--porcelain'), '');
    unchanged(fixture);
  });
}

test('checks use the backend directory and exact release Docker version', () => {
  const root = resolve(tmpdir(), 'release-command-test');
  const calls = [];
  runReleaseChecks(root, 'v0.1.0', (...args) => calls.push(args));
  assert.deepEqual(calls, [
    ['go', ['test', './...'], { cwd: resolve(root, 'backend'), stdio: 'inherit' }],
    ['docker', ['build', '--build-arg', 'VERSION=v0.1.0', '-t', 'nox-backend:release-check', './backend'],
      { cwd: root, stdio: 'inherit' }],
  ]);
});

test('checks changing other files abort and preserve unrelated changes', async t => {
  const fixture = await repository(t);
  await assert.rejects(release(fixture.root, '0.1.0', {
    check: () => writeFile(resolve(fixture.root, 'tracked.txt'), 'check changed this\n'),
  }), /fora de VERSION/);
  assert.equal(await readFile(resolve(fixture.root, 'tracked.txt'), 'utf8'), 'check changed this\n');
  assert.equal(await readFile(resolve(fixture.root, 'VERSION'), 'utf8'), fixture.original);
  unchanged(fixture);
});

test('checks cannot silently change the release version', async t => {
  const fixture = await repository(t);
  await assert.rejects(release(fixture.root, '0.1.0', {
    check: () => writeFile(resolve(fixture.root, 'VERSION'), '0.2.0\n'),
  }), /nao corresponde/);
  assert.equal(await readFile(resolve(fixture.root, 'VERSION'), 'utf8'), fixture.original);
  unchanged(fixture);
});

for (const input of ['0.1.0', 'v0.1.0-rc.1']) {
  test(`valid release ${input} creates the synchronized commit and annotated tag and pushes both`, async t => {
    const fixture = await repository(t);
    const version = input.replace(/^v/, '');
    const tag = `v${version}`;
    const branch = input.startsWith('v') ? 'release/prerelease' : 'main';
    if (branch !== 'main') git(fixture.root, 'checkout', '-b', branch);
    git(fixture.root, 'tag', 'unrelated-tag');
    let checked = false;
    assert.equal(await release(fixture.root, input, { async check(root, checkedTag) {
      checked = true;
      assert.equal(checkedTag, tag);
      assert.equal(await readFile(resolve(root, 'VERSION'), 'utf8'), `${version}\n`);
      assert.equal(git(root, 'rev-parse', 'HEAD'), fixture.head);
      assert.equal(git(root, 'tag', '--list', tag), '');
    } }), tag);
    assert.ok(checked);
    const head = git(fixture.root, 'rev-parse', 'HEAD');
    assert.notEqual(head, fixture.head);
    assert.equal(git(fixture.root, 'log', '-1', '--format=%s'), `chore: release ${tag}`);
    assert.equal(git(fixture.root, 'diff-tree', '--no-commit-id', '--name-only', '-r', 'HEAD'), 'VERSION');
    assert.equal(git(fixture.root, 'cat-file', '-t', tag), 'tag');
    assert.equal(git(fixture.root, 'rev-parse', `${tag}^{commit}`), head);
    assert.equal(git(fixture.root, 'show', `${tag}:VERSION`), version);
    assert.equal(git(fixture.root, 'status', '--porcelain'), '');
    assert.equal(git(fixture.origin, 'rev-parse', `refs/heads/${branch}`), head);
    assert.equal(git(fixture.origin, 'rev-parse', `${tag}^{commit}`), head);
    assert.equal(git(fixture.origin, 'tag', '--list'), tag);
  });
}

test('push failure preserves local release and atomic push leaves remote refs unchanged', async t => {
  const fixture = await repository(t);
  git(fixture.origin, 'config', 'receive.denyNonFastForwards', 'true');
  // Advance origin independently so its branch rejects our release commit.
  const tree = git(fixture.root, 'rev-parse', 'HEAD^{tree}');
  const remoteHead = git(fixture.root, '-c', 'user.name=Release test', '-c', 'user.email=release@example.invalid',
    'commit-tree', tree, '-p', fixture.head, '-m', 'Concurrent remote commit');
  git(fixture.root, 'push', 'origin', `${remoteHead}:refs/heads/main`);
  await assert.rejects(release(fixture.root, '0.1.0', noChecks), error => {
    assert.match(error.message, /commit e a tag v0.1.0 ficaram locais/);
    assert.match(error.message, /git push --atomic origin/);
    assert.match(error.message, /HEAD:refs\/heads\/main/);
    assert.match(error.message, /refs\/tags\/v0.1.0:refs\/tags\/v0.1.0/);
    return true;
  });
  const head = git(fixture.root, 'rev-parse', 'HEAD');
  assert.notEqual(head, fixture.head);
  assert.equal(git(fixture.root, 'rev-parse', 'v0.1.0^{commit}'), head);
  assert.equal(git(fixture.root, 'cat-file', '-t', 'v0.1.0'), 'tag');
  assert.equal(await readFile(resolve(fixture.root, 'VERSION'), 'utf8'), '0.1.0\n');
  assert.equal(git(fixture.root, 'status', '--porcelain'), '');
  assert.equal(git(fixture.origin, 'rev-parse', 'refs/heads/main'), remoteHead);
  assert.equal(git(fixture.origin, 'tag', '--list'), '');
});

test('failed commit restores VERSION and index without tag or push', async t => {
  const fixture = await repository(t);
  git(fixture.root, 'config', 'core.hooksPath', resolve(fixture.root, '.git/hooks'));
  await writeFile(resolve(fixture.root, '.git/hooks/pre-commit'), '#!/bin/sh\nexit 1\n', { mode: 0o755 });
  await assert.rejects(release(fixture.root, '0.1.0', noChecks));
  assert.equal(await readFile(resolve(fixture.root, 'VERSION'), 'utf8'), fixture.original);
  assert.equal(git(fixture.root, 'status', '--porcelain'), '');
  unchanged(fixture);
});

test('failed tag creation preserves the release commit and prints recovery', async t => {
  const fixture = await repository(t);
  git(fixture.root, 'config', 'tag.gpgsign', 'true');
  git(fixture.root, 'config', 'gpg.program', resolve(fixture.root, 'missing-gpg'));
  await assert.rejects(release(fixture.root, '0.1.0', noChecks), /Commit de release mantido localmente.*Recovery/);
  assert.notEqual(git(fixture.root, 'rev-parse', 'HEAD'), fixture.head);
  assert.equal(await readFile(resolve(fixture.root, 'VERSION'), 'utf8'), '0.1.0\n');
  assert.equal(git(fixture.root, 'tag', '--list'), '');
  assert.equal(git(fixture.root, 'status', '--porcelain'), '');
  assert.equal(git(fixture.origin, 'rev-parse', 'refs/heads/main'), fixture.head);
});

test('VERSION and CI tags use the same strict version validation', () => {
  for (const version of ['0.1.0', '1.2.3-rc.1', '1.2.3-0', '1.2.3-01alpha']) {
    assert.equal(normalizeVersion(`v${version}`), version);
    assert.equal(parseVersionFile(`${version}\r\n`), version);
    assert.equal(validateReleaseTag(`v${version}`, `${version}\n`), version);
  }
  for (const contents of ['v0.1.0\n', '0.1.0\n\n', ' 0.1.0\n', '0.1.0 \n', '0.1.0\nextra']) {
    assert.throws(() => parseVersionFile(contents));
  }
  for (const tag of ['0.1.0', 'vv0.1.0', 'v01.1.0', 'v0.1.0\n', 'v0.1.0+build']) {
    assert.throws(() => validateReleaseTag(tag, '0.1.0\n'));
  }
  assert.throws(() => validateReleaseTag('v0.2.0', '0.1.0\n'), /nao corresponde/);
});

test('release CLI rejects missing or extra arguments without touching the real repository', () => {
  for (const args of [[], ['0.1.0', 'extra']]) {
    const result = spawnSync(process.execPath, [fileURLToPath(new URL('./release.mjs', import.meta.url)), ...args], { encoding: 'utf8' });
    assert.equal(result.status, 1);
    assert.match(result.stderr, /Uso: npm run release/);
  }
});

test('CI validation CLI accepts the current tag and refuses a mismatch', async () => {
  const contents = await readFile(new URL('../VERSION', import.meta.url), 'utf8');
  const version = parseVersionFile(contents);
  const script = fileURLToPath(new URL('./check-release-version.mjs', import.meta.url));
  const valid = spawnSync(process.execPath, [script, `v${version}`], { encoding: 'utf8' });
  assert.equal(valid.status, 0, valid.stderr);
  const mismatched = spawnSync(process.execPath, [script, version === '0.0.0' ? 'v0.0.1' : 'v0.0.0'], { encoding: 'utf8' });
  assert.equal(mismatched.status, 1);
  assert.match(mismatched.stderr, /nao corresponde/);
});

for (const [tag, prerelease, expectedTags] of [
  ['v1.0.2', false, ['ghcr.io/mapherez/nox-backend:v1.0.2', 'ghcr.io/mapherez/nox-backend:latest']],
  ['v1.1.0-rc.1', true, ['ghcr.io/mapherez/nox-backend:v1.1.0-rc.1']],
  ['v1.1.0-0', true, ['ghcr.io/mapherez/nox-backend:v1.1.0-0']],
]) {
  test(`${tag}: image tags, four OCI labels and GitHub prerelease flag agree`, async () => {
    const revision = 'a'.repeat(40);
    const created = new Date('2026-10-06T12:34:56.000Z');
    const metadata = releaseMetadata(tag, `${tag.slice(1)}\n`, revision, created);
    assert.deepEqual(metadata.tags.split('\n'), expectedTags);
    assert.equal(metadata.prerelease, prerelease);
    assert.deepEqual(Object.fromEntries(metadata.labels.split('\n').map(line => line.split('='))), {
      'org.opencontainers.image.version': tag,
      'org.opencontainers.image.revision': revision,
      'org.opencontainers.image.source': 'https://github.com/mapherez/nox-backend',
      'org.opencontainers.image.created': '2026-10-06T12:34:56.000Z',
    });

    // Exercise the actual workflow's release API call using the generated classification.
    const workflow = (await readFile(new URL('../.github/workflows/release.yml', import.meta.url), 'utf8')).replaceAll('\r\n', '\n');
    const body = workflow.split('      - name: Create GitHub Release with generated notes')[1].split('          script: |\n')[1];
    assert.ok(body, 'GitHub Release creation script must exist');
    const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;
    let request;
    await new AsyncFunction('github', 'context', 'process', body)(
      { rest: { repos: { async createRelease(payload) { request = payload; } } } },
      { repo: { owner: 'mapherez', repo: 'nox-backend' } },
      { env: { RELEASE_TAG: tag, RELEASE_PRERELEASE: String(metadata.prerelease) } },
    );
    assert.equal(request.tag_name, tag);
    assert.equal(request.prerelease, prerelease);
    assert.equal(request.generate_release_notes, true);
  });
}

test('image metadata refuses invalid tags, mismatched VERSION and invalid revision', () => {
  assert.throws(() => releaseMetadata('v1.01.0', '1.01.0\n', 'a'.repeat(40)), /Versao invalida/);
  assert.throws(() => releaseMetadata('v1.1.0-rc.1', '1.1.0\n', 'a'.repeat(40)), /nao corresponde/);
  assert.throws(() => releaseMetadata('v1.0.2', '1.0.2\n', 'short-sha'), /SHA completo/);
});

test('metadata CLI exports exact build tags, UTC creation time and GitHub classification', async t => {
  const root = await mkdtemp(resolve(tmpdir(), 'nox-backend-metadata-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  await mkdir(resolve(root, 'scripts'));
  for (const script of ['release.mjs', 'release-metadata.mjs']) {
    await copyFile(new URL(`./${script}`, import.meta.url), resolve(root, 'scripts', script));
  }
  for (const version of ['1.0.2', '1.1.0-rc.1']) {
    await writeFile(resolve(root, 'VERSION'), `${version}\n`);
    const output = resolve(root, 'outputs');
    await writeFile(output, '');
    const before = Date.now();
    const result = spawnSync(process.execPath, [resolve(root, 'scripts/release-metadata.mjs'), `v${version}`], {
      encoding: 'utf8', env: { ...process.env, GITHUB_OUTPUT: output, RELEASE_REVISION: 'b'.repeat(40) },
    });
    const after = Date.now();
    assert.equal(result.status, 0, result.stderr);
    // Read outputs using the same multiline format consumed by GitHub Actions.
    const lines = (await readFile(output, 'utf8')).trimEnd().split('\n');
    const outputs = {};
    while (lines.length) {
      const [key, delimiter] = lines.shift().split('<<');
      const end = lines.indexOf(delimiter);
      assert.ok(end >= 0, 'output delimiter must be closed');
      outputs[key] = lines.splice(0, end).join('\n');
      lines.shift();
    }
    assert.equal(outputs.prerelease, version === '1.0.2' ? 'false' : 'true');
    assert.deepEqual(outputs.tags.split('\n'), version === '1.0.2'
      ? ['ghcr.io/mapherez/nox-backend:v1.0.2', 'ghcr.io/mapherez/nox-backend:latest']
      : ['ghcr.io/mapherez/nox-backend:v1.1.0-rc.1']);
    const created = outputs.labels.split('\n').find(label => label.startsWith('org.opencontainers.image.created=')).split('=')[1];
    assert.equal(new Date(created).toISOString(), created);
    assert.ok(Date.parse(created) >= before && Date.parse(created) <= after);
  }
});

test('workflow feeds generated tags and labels to Buildx and classification to GitHub', async () => {
  const workflow = (await readFile(new URL('../.github/workflows/release.yml', import.meta.url), 'utf8')).replaceAll('\r\n', '\n');
  const build = workflow.split('      - name: Build and push backend image')[1]
    .split('      - name: Create GitHub Release')[0];
  assert.match(build, /tags: \$\{\{ steps\.metadata\.outputs\.tags \}\}/);
  assert.match(build, /labels: \$\{\{ steps\.metadata\.outputs\.labels \}\}/);
  assert.doesNotMatch(build, /nox-backend:latest/);
  assert.match(workflow, /RELEASE_PRERELEASE: \$\{\{ steps\.metadata\.outputs\.prerelease \}\}/);
  assert.match(workflow, /RELEASE_REVISION: \$\{\{ github\.sha \}\}/);
  assert.ok(workflow.indexOf('id: metadata') < workflow.indexOf('      - name: Build and push backend image'));
});
