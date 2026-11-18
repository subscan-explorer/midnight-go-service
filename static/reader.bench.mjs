import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createServer } from 'node:http';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { performance } from 'node:perf_hooks';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { ContractState } from '@midnight-ntwrk/midnight-js-protocol/compact-runtime';
import { stateFixture } from './fixtures/state-fixture.mjs';
import { compiledLedger, stateResult, replacer } from './state.mjs';
import * as contract from './fixtures/reader-031.js';

// Compare independent cold processes using identical real state and a local
// GraphQL response. Results exclude production network latency and script writes.
const runs = Number(process.env.READER_BENCH_RUNS ?? 10);
if (!Number.isSafeInteger(runs) || runs < 2 || runs > 100) throw new Error('READER_BENCH_RUNS must be between 2 and 100');
const exec = promisify(execFile);
const address = 'ab'.repeat(32);
const loader = fileURLToPath(new URL('./register-runtime.mjs', import.meta.url));
const reader = fileURLToPath(new URL('./read-state.mjs', import.meta.url));
const moduleURL = new URL('./fixtures/reader-031.js', import.meta.url);
const stateHex = stateFixture();
const response = JSON.stringify({ data: { contractAction: { __typename: 'ContractUpdate', state: stateHex } } });
const dir = await mkdtemp(join(tmpdir(), 'midnight-reader-bench-'));
let started, requested, responded;
const server = createServer((req, res) => {
  req.resume();
  requested = performance.now();
  res.setHeader('Content-Type', 'application/json');
  res.end(response);
  responded = performance.now();
});
try {
  // Preserve the old generated reader and loader sequence for comparisons after
  // future changes. The contract fixture is identical for both entry points.
  const legacy = `${dir}/generated_demo.ts`;
  await writeFile(`${dir}/package.json`, '{"type":"module"}');
  await writeFile(legacy, `
import * as Contract from '${moduleURL.href}';
import { readContractState, stateResult, compiledLedger, replacer } from '${new URL('./state.mjs', import.meta.url).href}';
const state = await readContractState('${address}', process.argv[2]);
const ledger = await compiledLedger(new URL('${moduleURL.href}'), Contract.ledger);
console.log(JSON.stringify(stateResult(state, ledger), replacer));
`);
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const endpoint = `http://127.0.0.1:${server.address().port}/graphql`;
  const env = { ...process.env, MIDNIGHT_NETWORK_ID: 'preview', INDEXER_RPC: endpoint, INDEXER_WS: endpoint.replace('http:', 'ws:'), TS_NODE_TRANSPILE_ONLY: 'true', TS_NODE_COMPILER_OPTIONS: '{"module":"NodeNext","moduleResolution":"NodeNext"}' };
  const results = { baseline: [], fixed: [] };
  let expected;
  for (let i = 0; i <= runs; i++) {
    // Alternate order to reduce systematic cache/temperature bias. Discard one
    // warm-up process for each mode; SDK imports remain cold in every child.
    for (const mode of i % 2 ? ['fixed', 'baseline'] : ['baseline', 'fixed']) {
      const args = mode === 'baseline'
        ? ['--import', loader, '--experimental-specifier-resolution=node', '--loader', 'ts-node/esm', legacy, '']
        : ['--import', loader, reader, address, '', fileURLToPath(moduleURL)];
      requested = responded = undefined;
      started = performance.now();
      const { stdout } = await exec(process.execPath, args, { env, timeout: 10000 });
      const finished = performance.now();
      expected ??= stdout;
      assert.equal(stdout, expected);
      assert.ok(requested && responded);
      if (i) results[mode].push({ total: finished - started, beforeRequest: requested - started, afterResponse: finished - responded });
    }
  }
  const median = values => {
    values.sort((a, b) => a - b);
    const middle = Math.floor(values.length / 2);
    return values.length % 2 ? values[middle] : (values[middle - 1] + values[middle]) / 2;
  };
  const summary = Object.fromEntries(Object.entries(results).map(([mode, samples]) => [mode, Object.fromEntries(['total', 'beforeRequest', 'afterResponse'].map(key => [key + 'MedianMs', +median(samples.map(sample => sample[key])).toFixed(2)]))]));
  const ledger = await compiledLedger(moduleURL, contract.ledger);
  const raw = Buffer.from(stateHex, 'hex');
  const deserialize = [], ledgerAndJSON = [];
  for (let i = 0; i < 101; i++) {
    const start = performance.now();
    const state = ContractState.deserialize(raw);
    const decoded = performance.now();
    JSON.stringify(stateResult(state, ledger), replacer);
    const finished = performance.now();
    if (i) {
      deserialize.push(decoded - start);
      ledgerAndJSON.push(finished - decoded);
    }
  }
  console.log(JSON.stringify({ node: process.version, platform: `${process.platform}/${process.arch}`, runs, stateBytes: raw.length, ...summary, reductionPercent: +(100 * (1 - summary.fixed.totalMedianMs / summary.baseline.totalMedianMs)).toFixed(1), warmDecodeMedianMs: +median(deserialize).toFixed(3), warmLedgerAndJSONMedianMs: +median(ledgerAndJSON).toFixed(3) }, null, 2));
} finally {
  if (server.listening) await new Promise(resolve => server.close(resolve));
  await rm(dir, { recursive: true, force: true });
}
