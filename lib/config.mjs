import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
import { parseArgs } from 'node:util';

export const defaults = Object.freeze({
  model: 'deepseek/deepseek-v4.1-flash',
  reasoning: 'high',
});

export function homeDirectory() {
  const directory = path.normalize(process.env.CODEX_OPENROUTER_HOME ?? path.join(os.homedir(), '.codex-openrouter'));
  if (!path.isAbsolute(directory) || directory === path.parse(directory).root) {
    throw new Error('CODEX_OPENROUTER_HOME must be an absolute, non-root directory.');
  }
  return directory;
}

function checkOwned(stat, file) {
  if (process.platform !== 'win32' &&
      (stat.uid !== process.getuid() || (stat.mode & 0o022) !== 0)) {
    throw new Error(`Refusing a path owned by another user or writable by others: ${file}`);
  }
}

export function ensureHome(directory = homeDirectory()) {
  // Only create the final directory; its parent must already be a trusted user directory.
  const parent = fs.statSync(path.dirname(directory));
  checkOwned(parent, path.dirname(directory));
  try {
    fs.mkdirSync(directory, { mode: 0o700 });
  } catch (error) {
    if (error.code !== 'EEXIST') throw error;
  }
  const stat = fs.lstatSync(directory);
  if (!stat.isDirectory() || stat.isSymbolicLink()) {
    throw new Error(`Refusing a symlink or non-directory: ${directory}`);
  }
  checkOwned(stat, directory);
  if (process.platform !== 'win32') fs.chmodSync(directory, 0o700);
  return directory;
}

export function validateConfig(config) {
  if (!config || Array.isArray(config) || typeof config !== 'object' ||
      Object.keys(config).some(key => !['model', 'reasoning'].includes(key))) {
    throw new Error('Config must contain only model and reasoning. Keep API keys in the environment.');
  }
  if (typeof config.model !== 'string' || config.model.length > 200 ||
      !/^~?[a-zA-Z0-9][a-zA-Z0-9._-]*\/[a-zA-Z0-9][a-zA-Z0-9._:~/-]*$/.test(config.model)) {
    throw new Error('Use a full OpenRouter model ID, such as deepseek/deepseek-v4.1-flash.');
  }
  if (!['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'].includes(config.reasoning)) {
    throw new Error('Reasoning must be none, minimal, low, medium, high, xhigh, or max.');
  }
  return { model: config.model, reasoning: config.reasoning };
}

export function readConfig(directory = homeDirectory()) {
  ensureHome(directory);
  const file = path.join(directory, 'config.json');
  let stat;
  try {
    stat = fs.lstatSync(file);
  } catch (error) {
    if (error.code === 'ENOENT') return { ...defaults };
    throw error;
  }
  if (!stat.isFile() || stat.isSymbolicLink() || stat.nlink !== 1 || stat.size > 4096) {
    throw new Error(`Config must be a regular file of at most 4 KiB, without symbolic or extra hard links: ${file}`);
  }
  checkOwned(stat, file);
  let config;
  try {
    config = JSON.parse(fs.readFileSync(file, 'utf8'));
  } catch {
    // JSON parser diagnostics can echo file contents, including accidentally pasted secrets.
    throw new Error(`Invalid JSON in ${file}`);
  }
  return validateConfig(config);
}

export function writeConfig(config, directory = homeDirectory()) {
  validateConfig(config);
  readConfig(directory);
  const file = path.join(directory, 'config.json');
  const temporary = path.join(directory, `.config-${randomUUID()}.tmp`);
  try {
    fs.writeFileSync(temporary, `${JSON.stringify(config, null, 2)}\n`, { flag: 'wx', mode: 0o600 });
    fs.renameSync(temporary, file);
  } finally {
    fs.rmSync(temporary, { force: true });
  }
}

export function parseSettings(args, existing) {
  const { values, positionals } = parseArgs({
    args,
    allowPositionals: true,
    options: { model: { type: 'string' }, reasoning: { type: 'string' }, help: { type: 'boolean', short: 'h' } },
  });
  if (positionals.length > 1 || (positionals.length && values.model)) {
    throw new Error('Supply one model, either as a positional argument or with --model.');
  }
  return {
    help: values.help,
    config: validateConfig({
      model: values.model ?? positionals[0] ?? existing.model,
      reasoning: values.reasoning ?? existing.reasoning,
    }),
  };
}
