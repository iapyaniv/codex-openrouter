#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { ensureHome, parseSettings, readConfig, writeConfig } from './lib/config.mjs';
import { runNode } from './lib/process.mjs';

function findNpm() {
  const directories = [path.dirname(process.execPath), ...(process.env.PATH ?? '').split(path.delimiter)]
    .filter(directory => path.isAbsolute(directory));
  const candidates = [];
  for (const directory of directories) {
    candidates.push(path.join(directory, 'node_modules', 'npm', 'bin', 'npm-cli.js'));
    try {
      const resolved = fs.realpathSync(path.join(directory, 'npm'));
      if (path.basename(resolved) === 'npm-cli.js') candidates.push(resolved);
    } catch (error) {
      if (!['ENOENT', 'ENOTDIR'].includes(error.code)) throw error;
    }
  }
  const npm = candidates.find(candidate => fs.existsSync(candidate));
  if (!npm) throw new Error('Cannot find npm-cli.js. Install Node.js 22+ with npm and put it on PATH.');
  const { version } = JSON.parse(fs.readFileSync(path.resolve(npm, '..', '..', 'package.json'), 'utf8'));
  if (Number(version.split('.')[0]) < 10) throw new Error('Installation requires npm 10 or newer.');
  return npm;
}

function codexEntrypoint(packageRoot) {
  const require = createRequire(path.join(packageRoot, 'package.json'));
  return path.join(path.dirname(require.resolve('@openai/codex/package.json')), 'bin', 'codex.js');
}

async function main(args) {
  if (args.includes('--help') || args.includes('-h')) {
    console.log('Usage: node install.mjs [MODEL | --model MODEL] [--reasoning high]');
    return;
  }
  if (Number(process.versions.node.split('.')[0]) < 22) throw new Error('Node.js 22+ is required.');
  if (!['darwin', 'linux', 'win32'].includes(process.platform) || !['x64', 'arm64'].includes(process.arch)) {
    throw new Error('Codex requires macOS, Linux, or Windows on x64 or ARM64.');
  }
  const npm = findNpm();
  const home = ensureHome();
  const { config } = parseSettings(args, readConfig(home));
  const source = path.dirname(fileURLToPath(import.meta.url));
  const temporary = fs.mkdtempSync(path.join(home, '.install-'));
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) =>
    !['OPENROUTER_API_KEY', 'OPENAI_API_KEY'].includes(key.toUpperCase())));
  const npmArgs = ['--ignore-scripts', '--no-audit', '--no-fund', '--registry=https://registry.npmjs.org'];
  try {
    console.log('Installing codex-openrouter and the pinned Codex CLI in your user directory...');
    const stage = path.join(temporary, 'package');
    fs.mkdirSync(stage, { mode: 0o700 });
    const manifest = JSON.parse(fs.readFileSync(path.join(source, 'package.json'), 'utf8'));
    for (const file of new Set([...manifest.files, 'README.md', 'LICENSE', 'package.json'])) {
      if (file !== 'dependency-lock.json') fs.cpSync(path.join(source, file), path.join(stage, file), { recursive: true });
    }
    fs.copyFileSync(path.join(source, 'package-lock.json'), path.join(stage, 'package-lock.json'));
    fs.copyFileSync(path.join(stage, 'package-lock.json'), path.join(stage, 'dependency-lock.json'));
    const resolved = await runNode(npm, ['ci', '--include=optional', ...npmArgs], { cwd: stage, env });
    if (resolved !== 0) throw new Error(`npm ci failed (${resolved}); your installation and defaults were not changed.`);
    // npm can succeed even when the optional native package failed integrity verification or installation.
    const staged = await runNode(codexEntrypoint(stage), ['--version'], { cwd: stage, env });
    if (staged !== 0) throw new Error('The locked Codex binary could not run; your installation and defaults were not changed.');
    // npm's hidden lock can retain skipped platform packages; packing must inventory the installed files.
    fs.rmSync(path.join(stage, 'node_modules', '.package-lock.json'), { force: true });
    manifest.bundleDependencies = Object.keys(manifest.dependencies);
    fs.writeFileSync(path.join(stage, 'package.json'), `${JSON.stringify(manifest, null, 2)}\n`);
    const packed = await runNode(npm, ['pack', '--pack-destination', temporary, '--offline', ...npmArgs], { cwd: stage, env });
    if (packed !== 0) throw new Error(`npm pack failed (${packed}).`);
    const archives = fs.readdirSync(temporary).filter(file => file.endsWith('.tgz'));
    if (archives.length !== 1) throw new Error('npm pack did not produce exactly one archive.');
    // An empty offline cache makes missing bundle contents fail instead of resolving unverified dependencies.
    const installCache = path.join(temporary, 'install-cache');
    fs.mkdirSync(installCache, { mode: 0o700 });
    const installed = await runNode(npm, [
      'install', '--global', '--prefix', home, '--include=optional', '--offline', '--cache', installCache,
      ...npmArgs, path.join(temporary, archives[0]),
    ], { cwd: temporary, env });
    if (installed !== 0) throw new Error(`npm install failed (${installed}); your defaults were not changed.`);
    const packageRoot = path.join(home, process.platform === 'win32' ? 'node_modules' : 'lib/node_modules', 'codex-openrouter');
    const verified = await runNode(codexEntrypoint(packageRoot), ['--version'], { cwd: temporary, env });
    if (verified !== 0) throw new Error('The Codex binary could not run; your defaults were not changed. Rerun the installer.');
    writeConfig(config, home);
    const bin = process.platform === 'win32' ? home : path.join(home, 'bin');
    console.log(`\nDefault: ${config.model} (${config.reasoning})`);
    console.log(`Config: ${path.join(home, 'config.json')}`);
    console.log(`Add this directory to your user PATH: ${bin}`);
    console.log('Set OPENROUTER_API_KEY, then run codex-openrouter.');
  } finally {
    fs.rmSync(temporary, { recursive: true, force: true });
  }
}

try {
  await main(process.argv.slice(2));
} catch (error) {
  console.error(`codex-openrouter installer: ${error.message}`);
  process.exitCode = 1;
}
