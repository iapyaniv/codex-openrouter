#!/usr/bin/env node
import path from 'node:path';
import fs from 'node:fs';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { homeDirectory, parseSettings, readConfig, writeConfig } from './lib/config.mjs';
import { runNode } from './lib/process.mjs';

export function codexArguments(config, args) {
  const quote = JSON.stringify;
  // Command-backed auth enables OpenRouter's model catalog; a direct Node helper avoids shell expansion.
  const auth = `{ command = ${quote(process.execPath)}, args = [${quote(fileURLToPath(new URL('./auth.mjs', import.meta.url)))}] }`;
  const provider = `{ name = "OpenRouter", base_url = "https://openrouter.ai/api/v1", wire_api = "responses", auth = ${auth} }`;
  return [
    '-c', 'model_provider="codex_openrouter"',
    '-c', `model_providers.codex_openrouter=${provider}`,
    '-c', `model=${quote(config.model)}`,
    '-c', `model_reasoning_effort=${quote(config.reasoning)}`,
    '-c', 'check_for_update_on_startup=false',
    // Auth keeps the original environment, so shell filtering and disabling legacy snapshots are both needed.
    '-c', 'shell_environment_policy.ignore_default_excludes=false',
    '-c', 'shell_environment_policy.set.OPENROUTER_API_KEY=""',
    '-c', 'features.shell_snapshot=false',
    ...args,
  ];
}

function isUpdateCommand(args) {
  const valueOptions = new Set([
    '-c', '--config', '--enable', '--disable', '--remote', '--remote-auth-token-env',
    '-i', '--image', '-m', '--model', '--local-provider', '-p', '--profile',
    '-s', '--sandbox', '-C', '--cd', '--add-dir', '-a', '--ask-for-approval',
  ]);
  const flags = new Set([
    '--strict-config', '--oss', '--approve-for-me', '--dangerously-bypass-approvals-and-sandbox',
    '--dangerously-bypass-hook-trust', '--worktree', '--search', '--no-alt-screen',
    '-h', '--help', '-V', '--version',
  ]);
  for (let index = 0; index < args.length; index++) {
    const arg = args[index];
    if (arg === '--') return false;
    if (!arg.startsWith('-')) return arg === 'update';
    const option = arg.startsWith('--') ? arg.split('=', 1)[0] : arg.slice(0, 2);
    if (valueOptions.has(option)) {
      if (arg === option) {
        index++;
        // Codex's separated --image values continue until the next option; attached values do not.
        if (option === '-i' || option === '--image') {
          while (index + 1 < args.length && !args[index + 1].startsWith('-')) index++;
        }
      }
    } else if (!flags.has(arg)) {
      return false;
    }
  }
  return false;
}

async function main(args) {
  if (isUpdateCommand(args)) {
    console.log('To update codex-openrouter and its pinned Codex CLI, run node install.mjs from an updated checkout. Saved defaults are preserved.');
    return 0;
  }
  const config = readConfig();
  if (args[0] === '--set-default') {
    if (args.length === 1) throw new Error('Usage: codex-openrouter --set-default MODEL [--reasoning high]');
    const settings = parseSettings(args.slice(1), config);
    if (settings.help) throw new Error('Usage: codex-openrouter --set-default MODEL [--reasoning high]');
    writeConfig(settings.config);
    console.log(`Default: ${settings.config.model} (${settings.config.reasoning})`);
    return 0;
  }
  if (args[0] === '--show-config' && args.length === 1) {
    console.log(path.join(homeDirectory(), 'config.json'));
    console.log(JSON.stringify(config, null, 2));
    return 0;
  }
  const informational = args.length === 1 && ['--help', '-h', '--version', '-V'].includes(args[0]);
  if (!informational && !process.env.OPENROUTER_API_KEY?.trim()) {
    throw new Error('Set OPENROUTER_API_KEY in your environment before starting codex-openrouter.');
  }
  const require = createRequire(import.meta.url);
  const codex = path.join(path.dirname(require.resolve('@openai/codex/package.json')), 'bin', 'codex.js');
  return runNode(codex, codexArguments(config, args));
}

if (process.argv[1] && fileURLToPath(import.meta.url) === fs.realpathSync(process.argv[1])) {
  try {
    process.exitCode = await main(process.argv.slice(2));
  } catch (error) {
    console.error(`codex-openrouter: ${error.message}`);
    process.exitCode = 1;
  }
}
