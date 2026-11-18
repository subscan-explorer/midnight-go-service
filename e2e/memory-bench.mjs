// Build both revisions' midnight-server (linux/amd64) and midnight-server-native
// with the same Go version before running. The before/after trees exclude node_modules.
// docker build --platform linux/amd64 -f e2e/memory.Dockerfile -t midnight-go:memory-bench .
// MEMORY_BENCH_BEFORE=/absolute/before MEMORY_BENCH_AFTER=/absolute/after \
// MEMORY_BENCH_OUTPUT=/absolute/results.json node --import ./static/register-runtime.mjs e2e/memory-bench.mjs
import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';
import { createServer } from 'node:http';
import { once } from 'node:events';
import {
  cp,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import * as runtime from '@midnight-ntwrk/compact-runtime';
import { stateFixture } from '../static/fixtures/state-fixture.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const before = process.env.MEMORY_BENCH_BEFORE;
const after = process.env.MEMORY_BENCH_AFTER;
assert.ok(before && after, 'Set MEMORY_BENCH_BEFORE and MEMORY_BENCH_AFTER');
const image = process.env.MEMORY_BENCH_IMAGE ?? 'midnight-go:memory-bench';
const runs = Number(process.env.MEMORY_BENCH_RUNS ?? 3);
assert.ok(Number.isSafeInteger(runs) && runs > 0 && runs <= 10);
const exec = promisify(execFile);
const command = (name, args) =>
  exec(name, args, { maxBuffer: 4 << 20, timeout: 30000 });
const directory = await mkdtemp(path.join(tmpdir(), 'midnight-memory-bench-'));
const address = '0x' + 'ab'.repeat(32),
  largeAddress = '0x' + 'ef'.repeat(32),
  hash = '0x' + 'cd'.repeat(32);
const body = (state) =>
  JSON.stringify({
    data: { contractAction: { __typename: 'ContractUpdate', state } },
  });
const small = body(stateFixture(2n)),
  historical = body(stateFixture(1n));
const keyType = new runtime.CompactTypeUnsignedInteger(65535n, 2);
let map = new runtime.StateMap();
for (let i = 0; i < 2048; i++) {
  const text = String(i).padStart(8, '0') + 'x'.repeat(4088);
  const cell = runtime.StateValue.newCell({
    value: runtime.CompactTypeOpaqueString.toValue(text),
    alignment: runtime.CompactTypeOpaqueString.alignment(),
  });
  map = map.insert(
    { value: keyType.toValue(BigInt(i)), alignment: keyType.alignment() },
    cell,
  );
}
const largeState = new runtime.ContractState();
largeState.data = new runtime.ChargedState(runtime.StateValue.newMap(map));
const rawLarge = Buffer.from(largeState.serialize()).toString('hex');
const large = body(rawLarge);
const fixtureDir = path.join(directory, 'fixtures');
const compiled = path.join(
  fixtureDir,
  'verified',
  largeAddress,
  'src',
  'managed',
  'contract',
);
await mkdir(compiled, { recursive: true });
await writeFile(
  path.join(compiled, 'index.js'),
  `import * as runtime from '@midnight-ntwrk/compact-runtime';
runtime.checkRuntimeVersion('0.16.0');
export function ledger(state){const value=state instanceof runtime.StateValue?state:state.state;const map=value.asMap();return {items:map.keys().map(key=>runtime.CompactTypeOpaqueString.fromValue(map.get(key).asCell().value))};}
`,
);
const indexer = createServer(async (req, res) => {
  try {
    let data = '';
    for await (const chunk of req) data += chunk;
    const variables = JSON.parse(data).variables;
    await delay(30);
    res.setHeader('Content-Type', 'application/json');
    res.end(
      variables.address === 'ef'.repeat(32)
        ? large
        : variables.offset
          ? historical
          : small,
    );
  } catch {
    res.destroy();
  }
});
await new Promise((resolve) => indexer.listen(0, '0.0.0.0', resolve));
const endpoint = (host) => `http://${host}:${indexer.address().port}/graphql`;
const sources = { before, after };
const modes = process.env.MEMORY_BENCH_MODES?.split(',') ?? ['before', 'after'];
const results = [];
const reference = new Map();
const cases = [
  { name: 'maintenance', requests: 12, concurrency: 4 },
  { name: 'large-ledger-8MiB', requests: 4, concurrency: 2 },
  { name: 'burst', requests: 16, concurrency: 16 },
  { name: 'mixed-native-compile', requests: 6, concurrency: 2, native: true },
];
const enabled = process.env.MEMORY_BENCH_CASES?.split(',');
const fixturesDigest = createHash('sha256').update(large).digest('hex');

function environment(staticDir, tempDir, url) {
  return {
    ...process.env,
    NETWORK_ID: 'preview',
    PORT: '8080',
    STATIC_DIR: staticDir,
    TEMP_DIR: tempDir,
    INDEXER_RPC: url,
    INDEXER_WS: url.replace('http:', 'ws:'),
    MIDNIGHT_PREVIEW_INDEXER_RPC: url,
    MIDNIGHT_PREVIEW_INDEXER_WS: url.replace('http:', 'ws:'),
    MIDNIGHT_PREVIEW_RPC_NODE: url,
    MIDNIGHT_HTTP_WRITE_TIMEOUT: '3m',
  };
}
async function nativeSample(pid) {
  const { stdout } = await command('ps', ['-axo', 'pid=,ppid=,rss=,comm=']);
  const rows = stdout
    .trim()
    .split('\n')
    .map((line) => line.trim().split(/\s+/));
  const pids = new Set([String(pid)]);
  for (let changed = true; changed; ) {
    changed = false;
    for (const row of rows)
      if (pids.has(row[1]) && !pids.has(row[0])) {
        pids.add(row[0]);
        changed = true;
      }
  }
  return rows
    .filter((row) => pids.has(row[0]))
    .reduce((sum, row) => sum + Number(row[2]) * 1024, 0);
}
async function prepareNative(source) {
  const work = await mkdtemp(path.join(directory, 'native-'));
  await cp(path.join(source, 'static'), path.join(work, 'static'), {
    recursive: true,
  });
  await cp(
    path.join(fixtureDir, 'verified'),
    path.join(work, 'static', 'verified'),
    { recursive: true },
  );
  await cp(path.join(source, 'package.json'), path.join(work, 'package.json'));
  await symlink(
    path.join(root, 'node_modules'),
    path.join(work, 'node_modules'),
    'dir',
  );
  return work;
}
async function one(mode, test, iteration) {
  let container,
    child,
    work,
    baseURL,
    readMetrics,
    stopSample = false,
    sampler,
    peak = 0,
    logs = '';
  const source = sources[mode];
  try {
    if (test.native) {
      work = await prepareNative(source);
      const reservation = createServer();
      await new Promise((resolve) =>
        reservation.listen(0, '127.0.0.1', resolve),
      );
      const port = reservation.address().port;
      await new Promise((resolve) => reservation.close(resolve));
      child = spawn(path.join(source, 'midnight-server-native'), [], {
        cwd: work,
        env: {
          ...environment(
            path.join(work, 'static'),
            path.join(work, 'temp'),
            endpoint('127.0.0.1'),
          ),
          PORT: String(port),
        },
        stdio: ['ignore', 'ignore', 'pipe'],
      });
      child.stderr.on('data', (chunk) => {
        logs = (logs + chunk).slice(-8192);
      });
      baseURL = `http://127.0.0.1:${port}`;
      sampler = (async () => {
        while (!stopSample) {
          peak = Math.max(peak, await nativeSample(child.pid));
          await delay(20);
        }
      })();
      readMetrics = async () => ({
        peakBytes: peak,
        metric:
          'sampled sum of Go and descendant RSS (20ms plus ps overhead); shared pages may be double-counted',
      });
    } else {
      container = 'midnight-memory-' + randomUUID().slice(0, 12);
      const env = environment(
        '/work/static',
        '/work/temp',
        endpoint('host.docker.internal'),
      );
      const args = [
        'run',
        '-d',
        '--platform',
        'linux/amd64',
        '--name',
        container,
        '--memory=4g',
        '--cpus=2',
        '-p',
        '127.0.0.1::8080',
        '-v',
        source + ':/source:ro',
        '-v',
        fixtureDir + ':/fixtures:ro',
      ];
      for (const key of [
        'NETWORK_ID',
        'PORT',
        'STATIC_DIR',
        'TEMP_DIR',
        'INDEXER_RPC',
        'INDEXER_WS',
        'MIDNIGHT_PREVIEW_INDEXER_RPC',
        'MIDNIGHT_PREVIEW_INDEXER_WS',
        'MIDNIGHT_PREVIEW_RPC_NODE',
        'MIDNIGHT_HTTP_WRITE_TIMEOUT',
      ])
        args.push('-e', `${key}=${env[key]}`);
      args.push(
        '--entrypoint',
        'sh',
        image,
        '-c',
        'mkdir -p /work; cp -a /source/static /work/static; mkdir -p /work/static/verified; cp -a /fixtures/verified/. /work/static/verified/; cp /source/package.json /work/package.json; ln -s /bench/node_modules /work/node_modules; exec /source/midnight-server',
      );
      await command('docker', args);
      const { stdout } = await command('docker', [
        'port',
        container,
        '8080/tcp',
      ]);
      baseURL = 'http://' + stdout.trim();
      readMetrics = async () => {
        const { stdout } = await command('docker', [
          'exec',
          container,
          'sh',
          '-c',
          'cat /sys/fs/cgroup/memory.peak; cat /sys/fs/cgroup/memory.current; cat /sys/fs/cgroup/memory.events; cat /sys/fs/cgroup/memory.stat',
        ]);
        const lines = stdout.trim().split('\n');
        const fields = Object.fromEntries(
          lines.slice(2).map((line) => line.split(' ')),
        );
        return {
          metric:
            'cgroup v2 memory.peak; includes Go, readers and accounted file cache',
          peakBytes: Number(lines[0]),
          postLoadBytes: Number(lines[1]),
          anonBytes: Number(fields.anon),
          fileBytes: Number(fields.file),
          oomKills: Number(fields.oom_kill),
        };
      };
    }
    for (let i = 0; ; i++) {
      try {
        const r = await fetch(baseURL + '/contract/state', {
          signal: AbortSignal.timeout(500),
        });
        await r.text();
        if (r.status === 400) break;
      } catch {}
      if (i === 100) throw new Error('server not ready ' + logs);
      await delay(50);
    }
    const initial = await readMetrics();
    let next = 0,
      success = 0,
      rejected = 0;
    const samples = [];
    const started = performance.now();
    const compiling = test.native
      ? (async () => {
          const source = await readFile(
            path.join(root, 'e2e', 'contracts', 'hello-world.compact'),
            'utf8',
          );
          const response = await fetch(baseURL + '/verify', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              contract_addr: '0x' + '12'.repeat(32),
              source_code: source,
              compiler_version: '0.31.1',
              source_type: 'single_file',
              verification_keys: { storeMessage: 'wrong fingerprint' },
            }),
            signal: AbortSignal.timeout(120000),
          });
          assert.equal(response.status, 200);
          const result = await response.json();
          assert.equal(result.verified_status, 'mismatch');
          assert.match(result.message, /verify key mismatch/);
        })()
      : Promise.resolve();
    await Promise.all(
      Array.from({ length: test.concurrency }, async () => {
        for (;;) {
          const index = next++;
          if (index >= test.requests) break;
          const largeRead = test.name === 'large-ledger-8MiB';
          const url =
            baseURL +
            '/contract/state?contract_addr=' +
            (largeRead ? largeAddress : address) +
            (index % 2 ? '&block_hash=' + hash : '');
          const start = performance.now();
          const response = await fetch(url, {
            signal: AbortSignal.timeout(150000),
          });
          const text = await response.text();
          samples.push(performance.now() - start);
          if (response.status === 503 && test.name === 'burst') {
            rejected++;
            continue;
          }
          assert.equal(
            response.status,
            200,
            `${mode} ${test.name}: ${text.slice(0, 200)}`,
          );
          const parsed = JSON.parse(text);
          if (largeRead) {
            assert.equal(parsed.data.items.length, 2048);
            assert.equal(parsed.data.items[0].length, 4096);
          } else
            assert.equal(
              parsed.maintenanceAuthority.counter,
              index % 2 ? 1 : 2,
            );
          const key = test.name + ':' + (index % 2);
          const digest = createHash('sha256').update(text).digest('hex');
          if (reference.has(key))
            assert.equal(digest, reference.get(key), 'response changed');
          else reference.set(key, digest);
          success++;
        }
      }).concat(compiling),
    );
    const elapsedMs = Math.round(performance.now() - started);
    await delay(200);
    const metrics = await readMetrics();
    if (!test.native) assert.equal(metrics.oomKills, 0);
    samples.sort((a, b) => a - b);
    const result = {
      mode,
      case: test.name,
      iteration,
      requests: test.requests,
      concurrency: test.concurrency,
      success,
      rejected,
      elapsedMs,
      p95Ms: Math.round(samples[Math.ceil(samples.length * 0.95) - 1]),
      initialBytes: initial.peakBytes,
      ...metrics,
    };
    results.push(result);
    console.log(JSON.stringify(result));
  } catch (error) {
    if (container) {
      const result = await command('docker', ['logs', container]).catch(() => ({
        stdout: '',
        stderr: '',
      }));
      logs = result.stdout + result.stderr;
    }
    throw new Error(`${mode} ${test.name}: ${error.message}\n${logs}`, {
      cause: error,
    });
  } finally {
    stopSample = true;
    if (sampler) await sampler;
    if (container) await command('docker', ['rm', '-f', container]);
    if (child && child.exitCode === null) {
      const exited = once(child, 'exit');
      child.kill('SIGTERM');
      await Promise.race([
        exited,
        delay(17000, undefined, { ref: false }).then(() => {
          if (child.exitCode === null) child.kill('SIGKILL');
        }),
      ]);
    }
    if (work) await rm(work, { recursive: true, force: true });
  }
}
try {
  for (const test of cases) {
    if (enabled && !enabled.includes(test.name)) continue;
    for (let i = 0; i < runs; i++)
      for (const mode of (i % 2
        ? ['after', 'before']
        : ['before', 'after']
      ).filter((mode) => modes.includes(mode)))
        await one(mode, test, i + 1);
  }
  const metadata = {
    before,
    after,
    runs,
    host: `${process.platform}/${process.arch}`,
    node: process.version,
    image,
    dockerImageId: (
      await command('docker', [
        'image',
        'inspect',
        image,
        '--format',
        '{{.Id}}',
      ])
    ).stdout.trim(),
    containerLimits: { memory: '4GiB', cpu: 2, platform: 'linux/amd64' },
    largeFixture: {
      rawBytes: rawLarge.length / 2,
      decodedPayloadBytes: 8 << 20,
      sha256: fixturesDigest,
    },
    results,
  };
  await writeFile(
    process.env.MEMORY_BENCH_OUTPUT ?? path.join(directory, 'results.json'),
    JSON.stringify(metadata, null, 2) + '\n',
  );
} finally {
  indexer.closeAllConnections();
  await new Promise((resolve) => indexer.close(resolve));
  await rm(directory, { recursive: true, force: true });
}
