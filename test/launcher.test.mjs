import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const source = fileURLToPath(new URL('..', import.meta.url));

function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'codex-openrouter-test-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const app = path.join(root, 'app space & café');
  fs.mkdirSync(app);
  for (const name of ['codex-openrouter.mjs', 'auth.mjs', 'lib']) {
    fs.cpSync(path.join(source, name), path.join(app, name), { recursive: true });
  }
  const codex = path.join(app, 'node_modules', '@openai', 'codex');
  fs.mkdirSync(path.join(codex, 'bin'), { recursive: true });
  fs.writeFileSync(path.join(codex, 'package.json'), JSON.stringify({ name: '@openai/codex', type: 'module' }));
  fs.writeFileSync(path.join(codex, 'bin', 'codex.js'),
    'console.log(JSON.stringify(process.argv.slice(2))); process.exitCode = Number(process.env.FAKE_CODEX_EXIT ?? 0);');
  const home = path.join(root, 'settings');
  const env = { ...process.env, CODEX_OPENROUTER_HOME: home, OPENROUTER_API_KEY: 'test-only-key' };
  const run = (...args) => spawnSync(process.execPath, [path.join(app, 'codex-openrouter.mjs'), ...args], {
    env, encoding: 'utf8', timeout: 10000,
  });
  return { root, app, home, env, run };
}

test('saved defaults survive later changes and CLI arguments arrive literally at Codex', t => {
  const { run, home, env } = fixture(t);
  let result = run('--set-default', 'vendor/first:free', '--reasoning', 'low');
  assert.equal(result.status, 0, result.stderr);
  result = run('--set-default', 'vendor/second');
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(JSON.parse(fs.readFileSync(path.join(home, 'config.json'), 'utf8')), {
    model: 'vendor/second', reasoning: 'low',
  });
  const args = ['exec', '-m', 'vendor/temporary', 'spaces "quotes" & $(echo nope) `echo nope` %PATH% café'];
  env.FAKE_CODEX_EXIT = '17';
  result = run(...args);
  assert.equal(result.status, 17, result.stderr);
  const forwarded = JSON.parse(result.stdout);
  assert.deepEqual(forwarded.slice(-args.length), args);
  assert.ok(forwarded.includes('model="vendor/second"'));
  assert.ok(forwarded.includes('model_reasoning_effort="low"'));
  assert.ok(forwarded.includes('check_for_update_on_startup=false'));
  assert.equal(JSON.parse(fs.readFileSync(path.join(home, 'config.json'), 'utf8')).model, 'vendor/second');
  if (process.platform !== 'win32') {
    assert.equal(fs.statSync(home).mode & 0o777, 0o700);
    assert.equal(fs.statSync(path.join(home, 'config.json')).mode & 0o777, 0o600);
  }
});

test('invalid model input cannot change persisted defaults or launch a process', t => {
  const { run, home } = fixture(t);
  assert.equal(run('--set-default', 'vendor/valid').status, 0);
  const before = fs.readFileSync(path.join(home, 'config.json'), 'utf8');
  for (const model of ['vendor/a"\napproval_policy="never', 'vendor/$(echo injected)', '--help', 'missing-provider']) {
    const result = run('--set-default', model);
    assert.notEqual(result.status, 0);
    assert.equal(result.stdout, '');
    assert.equal(fs.readFileSync(path.join(home, 'config.json'), 'utf8'), before);
  }
  const result = run('--set-default', 'vendor/valid', '--reasoning', 'invalid');
  assert.notEqual(result.status, 0);
  assert.equal(fs.readFileSync(path.join(home, 'config.json'), 'utf8'), before);
});

test('malformed or secret-bearing config fails without printing file contents', t => {
  const { run, home } = fixture(t);
  assert.equal(run('--set-default', 'vendor/model').status, 0);
  const secret = 'SYNTHETIC_SECRET_DO_NOT_ECHO';
  const file = path.join(home, 'config.json');
  for (const contents of [
    `{"model": "${secret}`,
    JSON.stringify({ model: 'vendor/model', reasoning: 'high', api_key: secret }),
    JSON.stringify({ model: `vendor/${secret}\n`, reasoning: 'high' }),
    ' '.repeat(4097),
  ]) {
    fs.writeFileSync(file, contents);
    const result = run('--show-config');
    assert.notEqual(result.status, 0);
    assert.equal(result.stdout, '');
    assert.ok(!result.stderr.includes(secret));
    assert.equal(fs.readFileSync(file, 'utf8'), contents);
  }
});

test('linked config files cannot redirect writes to another file', t => {
  const { run, root, home } = fixture(t);
  assert.equal(run('--set-default', 'vendor/model').status, 0);
  const file = path.join(home, 'config.json');
  const target = path.join(root, 'unrelated.json');
  const contents = fs.readFileSync(file, 'utf8');
  fs.writeFileSync(target, contents, { mode: 0o600 });
  fs.unlinkSync(file);
  fs.linkSync(target, file);
  assert.notEqual(run('--set-default', 'vendor/changed').status, 0);
  assert.equal(fs.readFileSync(target, 'utf8'), contents);
  fs.unlinkSync(file);
  try {
    fs.symlinkSync(target, file);
  } catch (error) {
    if (process.platform === 'win32' && error.code === 'EPERM') {
      t.diagnostic('File symlink check requires Windows Developer Mode; hardlink check passed.');
      return;
    }
    throw error;
  }
  assert.notEqual(run('--set-default', 'vendor/changed').status, 0);
  assert.equal(fs.readFileSync(target, 'utf8'), contents);
});

test('a symlink or Windows junction cannot redirect the managed directory', t => {
  const { run, root, home } = fixture(t);
  const target = path.join(root, 'target');
  fs.mkdirSync(target);
  fs.symlinkSync(target, home, process.platform === 'win32' ? 'junction' : 'dir');
  assert.notEqual(run('--set-default', 'vendor/model').status, 0);
  assert.deepEqual(fs.readdirSync(target), []);
});

test('config writable by other Unix users is refused', { skip: process.platform === 'win32' }, t => {
  const { run, home } = fixture(t);
  assert.equal(run('--set-default', 'vendor/model').status, 0);
  fs.chmodSync(path.join(home, 'config.json'), 0o666);
  assert.notEqual(run('--set-default', 'vendor/changed').status, 0);
  fs.chmodSync(path.join(home, 'config.json'), 0o600);
  fs.chmodSync(home, 0o777);
  assert.notEqual(run('--set-default', 'vendor/changed').status, 0);
});

test('credential helper treats the key as data; key-free operations work without credentials', t => {
  const { run, app, env, root } = fixture(t);
  const marker = path.join(root, 'executed');
  const key = `$(touch "${marker}") & %PATH% "literal"`;
  const helper = spawnSync(process.execPath, [path.join(app, 'auth.mjs')], {
    env: { ...env, OPENROUTER_API_KEY: key }, encoding: 'utf8',
  });
  assert.equal(helper.status, 0, helper.stderr);
  assert.equal(helper.stdout, key);
  assert.equal(helper.stderr, '');
  assert.equal(fs.existsSync(marker), false);
  env.OPENROUTER_API_KEY = '';
  assert.equal(run('--set-default', 'vendor/model').status, 0);
  assert.equal(run('--show-config').status, 0);
  assert.equal(run('--version').status, 0);
  assert.equal(run('--help').status, 0);
  const missing = run('exec', 'hello');
  assert.notEqual(missing.status, 0);
  assert.equal(missing.stdout, '');
});

test('updates use the installer while prompts and nested commands still reach Codex', t => {
  const { run, home, env } = fixture(t);
  assert.equal(run('--set-default', 'vendor/model', '--reasoning', 'low').status, 0);
  const configFile = path.join(home, 'config.json');
  const before = fs.readFileSync(configFile, 'utf8');
  env.OPENROUTER_API_KEY = '';
  env.FAKE_CODEX_EXIT = '17';
  for (const args of [
    ['update'],
    ['-c', 'x=y', 'update'],
    ['--config=x=y', '--no-alt-screen', 'update'],
    ['-cx=y', '-m', 'vendor/model', 'update'],
    ['--profile', 'update', '--search', 'update'],
    ['--image', 'one.png', 'two.png', '--search', 'update'],
    ['--image=one.png', 'update'],
    ['-ione.png', 'update'],
  ]) {
    const result = run(...args);
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout, 'To update codex-openrouter and its pinned Codex CLI, run node install.mjs from an updated checkout. Saved defaults are preserved.\n');
    assert.equal(result.stderr, '');
  }
  env.OPENROUTER_API_KEY = 'test-only-key';
  for (const args of [
    ['Please update the dependencies'],
    ['exec', 'update'],
    ['plugin', 'update', 'sample'],
    ['help', 'update'],
    ['--', 'update'],
    ['-c', 'x=y', '--', 'update'],
    ['exec', '--', 'update'],
    ['--profile', 'update', 'Explain this project'],
    ['--image', 'one.png', 'update'],
  ]) {
    const result = run(...args);
    assert.equal(result.status, 17, result.stderr);
    assert.deepEqual(JSON.parse(result.stdout).slice(-args.length), args);
  }
  assert.equal(fs.readFileSync(configFile, 'utf8'), before);
});

test('termination reaches a running child and preserves a failing exit status', {
  skip: process.platform === 'win32', timeout: 10000,
}, async t => {
  const { app, env } = fixture(t);
  fs.writeFileSync(path.join(app, 'node_modules', '@openai', 'codex', 'bin', 'codex.js'),
    'console.log("ready"); setInterval(() => {}, 1000);');
  const child = spawn(process.execPath, [path.join(app, 'codex-openrouter.mjs')], { env, stdio: ['ignore', 'pipe', 'pipe'] });
  t.after(() => { if (child.exitCode === null) child.kill('SIGKILL'); });
  const exited = once(child, 'exit');
  await once(child.stdout, 'data');
  child.kill('SIGTERM');
  const [code] = await exited;
  assert.equal(code, 143);
});
