import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import http from 'node:http';
import { execFile, spawnSync } from 'node:child_process';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';

const source = fileURLToPath(new URL('..', import.meta.url));
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'codex-openrouter-smoke-'));
const home = path.join(root, 'install space & café');
const codexHome = path.join(root, 'codex');
fs.mkdirSync(codexHome);
const includedEnvironment = [
  'PATH', 'PATHEXT', 'SYSTEMROOT', 'COMSPEC', 'HOME', 'USERPROFILE', 'TEMP', 'TMP', 'TMPDIR',
  'OPENROUTER_API_KEY', 'OTHER_*', 'AWS_*', 'SMOKE_*',
];
const legacyFilters = `exclude = ["SMOKE_EXCLUDED"]\ninclude_only = ${JSON.stringify(includedEnvironment)}`;
const canonicalFilters = `filters = { ${[
  'SMOKE_EXCLUDED = "exclude"',
  ...includedEnvironment.map(pattern => `${JSON.stringify(pattern)} = "include"`),
].join(', ')} }`;
const existingConfig = `model = "ordinary-model"
[features]
shell_snapshot = true
[shell_environment_policy]
inherit = "all"
ignore_default_excludes = true
${legacyFilters}
[shell_environment_policy.set]
SMOKE_SENTINEL = "kept"
[projects.${JSON.stringify(fs.realpathSync(root))}]
trust_level = "trusted"
`;
fs.writeFileSync(path.join(codexHome, 'config.toml'), existingConfig);
const syntheticSecrets = {
  OPENROUTER_API_KEY: 'local-smoke-test-only',
  OTHER_API_KEY: 'other-key-smoke-test-only',
  OTHER_TOKEN: 'other-token-smoke-test-only',
  AWS_SECRET_ACCESS_KEY: 'aws-secret-smoke-test-only',
};
const env = {
  ...process.env, ...syntheticSecrets, CODEX_OPENROUTER_HOME: home, CODEX_HOME: codexHome,
  OPENROUTER_API_KEY: '', OPENAI_API_KEY: '', SMOKE_EXCLUDED: 'must-be-excluded',
  SMOKE_INHERITED: 'inherited', OUTSIDE_INCLUDE_ONLY: 'must-be-excluded',
};
const probe = path.join(root, 'probe.mjs');
fs.writeFileSync(probe, `
if (${JSON.stringify(Object.keys(syntheticSecrets))}.some(name => process.env[name])) console.log('credential-exposed');
else if (process.env.SMOKE_SENTINEL !== 'kept' || process.env.SMOKE_INHERITED !== 'inherited' || process.env.SMOKE_EXCLUDED || process.env.OUTSIDE_INCLUDE_ONLY) console.log('existing-policy-lost');
else console.log('credential-isolated');
`);
const shellQuote = process.platform === 'win32'
  ? value => `'${value.replaceAll("'", "''")}'`
  : value => `'${value.replaceAll("'", "'\\''")}'`;
const probeCommand = `${process.platform === 'win32' ? '& ' : ''}${shellQuote(process.execPath)} ${shellQuote(probe)}`;

async function node(args) {
  const execution = promisify(execFile)(process.execPath, args, { env, cwd: root, encoding: 'utf8', timeout: 120000 });
  execution.child.stdin.end();
  const result = await execution;
  return result.stdout;
}

try {
  await node([path.join(source, 'install.mjs'), '--model', 'vendor/chosen', '--reasoning', 'medium']);
  const packageRoot = path.join(home, process.platform === 'win32' ? 'node_modules' : 'lib/node_modules', 'codex-openrouter');
  assert.deepEqual(
    JSON.parse(fs.readFileSync(path.join(packageRoot, 'dependency-lock.json'), 'utf8')),
    JSON.parse(fs.readFileSync(path.join(source, 'package-lock.json'), 'utf8')),
    'Installed package must retain the committed dependency integrity metadata',
  );
  const launcher = process.platform === 'win32'
    ? path.join(packageRoot, 'codex-openrouter.mjs')
    : path.join(home, 'bin', 'codex-openrouter');
  assert.match(await node([launcher, '--version']), /codex-cli /);
  assert.match(await node([launcher, '--help']), /Codex CLI/);
  const configFile = path.join(home, 'config.json');
  assert.deepEqual(JSON.parse(fs.readFileSync(configFile, 'utf8')), { model: 'vendor/chosen', reasoning: 'medium' });
  await node([launcher, '--set-default', 'vendor/changed']);
  await node([path.join(source, 'install.mjs')]);
  assert.deepEqual(JSON.parse(fs.readFileSync(configFile, 'utf8')), { model: 'vendor/changed', reasoning: 'medium' });
  assert.equal(fs.readFileSync(path.join(codexHome, 'config.toml'), 'utf8'), existingConfig);
  const beforeFailedInstall = fs.readFileSync(configFile, 'utf8');
  const invalidSource = path.join(root, 'invalid-integrity-source');
  fs.cpSync(source, invalidSource, {
    recursive: true,
    filter: entry => !['.git', 'node_modules'].includes(path.relative(source, entry).split(path.sep)[0]),
  });
  const invalidLockFile = path.join(invalidSource, 'package-lock.json');
  const invalidLock = JSON.parse(fs.readFileSync(invalidLockFile, 'utf8'));
  invalidLock.packages['node_modules/@openai/codex'].integrity = `sha512-${Buffer.alloc(64).toString('base64')}`;
  fs.writeFileSync(invalidLockFile, JSON.stringify(invalidLock));
  // A shipped lock alone did not enforce hashes in the previous global-install path.
  await assert.rejects(node([path.join(invalidSource, 'install.mjs')]), error => {
    if (process.env.npm_config_offline === 'true') {
      assert.match(error.stderr, /EINTEGRITY|integrity checksum failed|ENOTCACHED/);
      assert.ok(error.stderr.includes(invalidLock.packages['node_modules/@openai/codex'].resolved));
    } else {
      assert.match(error.stderr, /EINTEGRITY|integrity checksum failed/);
    }
    assert.match(error.stderr, /npm ci failed/);
    return true;
  });
  assert.equal(fs.readFileSync(configFile, 'utf8'), beforeFailedInstall);
  assert.match(await node([launcher, '--version']), /codex-cli /);
  const requests = [];
  let toolCallSent = false;
  let advertisedTools = [];
  let snapshotContainsSecret = false;
  const server = http.createServer(async (request, response) => {
    let body = '';
    for await (const chunk of request) body += chunk;
    const parsed = body ? JSON.parse(body) : null;
    requests.push({ url: request.url, authorization: request.headers.authorization, body: parsed });
    if (request.url.startsWith('/models')) {
      response.writeHead(200, { 'Content-Type': 'application/json' }).end('{"models":[]}');
      return;
    }
    const snapshots = path.join(codexHome, 'shell_snapshots');
    if (fs.existsSync(snapshots)) {
      for (const name of fs.readdirSync(snapshots)) {
        const file = path.join(snapshots, name);
        if (fs.statSync(file).isFile()) {
          const contents = fs.readFileSync(file);
          if (Object.values(syntheticSecrets).some(secret => contents.includes(secret))) snapshotContainsSecret = true;
        }
      }
    }
    advertisedTools = parsed?.tools?.map(tool => tool.name) ?? advertisedTools;
    let item = { id: 'msg_smoke', type: 'message', role: 'assistant', content: [{ type: 'output_text', text: 'smoke-ok' }] };
    if (!toolCallSent && advertisedTools.includes('exec_command')) {
      toolCallSent = true;
      item = {
        id: 'fc_probe', type: 'function_call', call_id: 'call_probe', name: 'exec_command',
        arguments: JSON.stringify({ cmd: probeCommand, login: false, max_output_tokens: 1000, yield_time_ms: 1000 }),
      };
    }
    response.writeHead(200, { 'Content-Type': 'text/event-stream' });
    for (const event of [
      { type: 'response.created', response: { id: 'resp_smoke' } },
      { type: 'response.output_item.done', output_index: 0, item },
      { type: 'response.completed', response: { id: 'resp_smoke', status: 'completed', output: [item], usage: { input_tokens: 1, output_tokens: 1, total_tokens: 2 } } },
    ]) response.write(`event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`);
    response.end();
  });
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  try {
    env.OPENROUTER_API_KEY = syntheticSecrets.OPENROUTER_API_KEY;
    const endpoint = `http://127.0.0.1:${server.address().port}`;
    for (const filters of [legacyFilters, canonicalFilters]) {
      const sessionConfig = existingConfig.replace(legacyFilters, filters);
      fs.writeFileSync(path.join(codexHome, 'config.toml'), sessionConfig);
      requests.length = 0;
      toolCallSent = false;
      advertisedTools = [];
      // The mock model executes only our generated probe; this avoids OS sandbox provisioning in CI.
      const output = await node([
        launcher, '-c', `model_providers.codex_openrouter.base_url="${endpoint}"`,
        'exec', '--ephemeral', '--skip-git-repo-check', '--sandbox', 'danger-full-access',
        '-m', 'vendor/session-override', 'Run the environment probe, then reply smoke-ok.',
      ]);
      assert.match(output, /smoke-ok/);
      const inference = requests.find(request => request.url === '/responses');
      assert.ok(inference, 'Codex must send a Responses request');
      assert.equal(inference.authorization, `Bearer ${syntheticSecrets.OPENROUTER_API_KEY}`);
      assert.equal(inference.body.model, 'vendor/session-override');
      assert.equal(inference.body.reasoning.effort, 'medium');
      assert.ok(toolCallSent, `Expected exec_command in ${JSON.stringify(advertisedTools)}`);
      const toolOutputs = requests.flatMap(request => request.body?.input ?? [])
        .filter(item => item.type === 'function_call_output');
      assert.match(JSON.stringify(toolOutputs), /credential-isolated/);
      assert.doesNotMatch(JSON.stringify(toolOutputs), /credential-exposed/);
      assert.equal(snapshotContainsSecret, false, 'Session snapshots must not persist ambient credentials');
      assert.equal(fs.readFileSync(path.join(codexHome, 'config.toml'), 'utf8'), sessionConfig);
    }
    fs.writeFileSync(path.join(codexHome, 'config.toml'), existingConfig);
  } finally {
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
    env.OPENROUTER_API_KEY = '';
  }
  assert.equal(fs.readFileSync(path.join(codexHome, 'config.toml'), 'utf8'), existingConfig);
  if (process.platform === 'win32') {
    const result = spawnSync('cmd.exe', ['/d', '/s', '/c', '""%CODEX_OPENROUTER_HOME%\\codex-openrouter.cmd" --version"'], {
      env, encoding: 'utf8', timeout: 10000, windowsVerbatimArguments: true,
    });
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /codex-cli /);
  } else {
    const execution = promisify(execFile)(launcher, ['--version'], { env, cwd: root, encoding: 'utf8', timeout: 10000 });
    execution.child.stdin.end();
    const result = await execution;
    assert.match(result.stdout, /codex-cli /);
  }
  console.log(`Installer, locked package, config changes, authentication, and tool credential isolation passed on ${process.platform}/${process.arch}.`);
} finally {
  fs.rmSync(root, { recursive: true, force: true });
}
