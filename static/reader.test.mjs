import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createServer } from 'node:http';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { stateFixture } from './fixtures/state-fixture.mjs';

const exec = promisify(execFile);
const address = 'ab'.repeat(32);
const hash = 'cd'.repeat(32);
const loader = fileURLToPath(new URL('./register-runtime.mjs', import.meta.url));
const reader = fileURLToPath(new URL('./read-state.mjs', import.meta.url));

test('fixed reader preserves latest, historical and legacy contract state', async t => {
  const requests = [];
  const server = createServer(async (req, res) => {
    let body = '';
    for await (const chunk of req) body += chunk;
    const request = JSON.parse(body);
    requests.push(request.variables);
    const counter = request.variables.offset ? 1n : 2n;
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ data: { contractAction: { __typename: 'ContractUpdate', state: stateFixture(counter) } } }));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => server.close(resolve)));
  const endpoint = `http://127.0.0.1:${server.address().port}/graphql`;
  const env = { ...process.env, MIDNIGHT_NETWORK_ID: 'preview', INDEXER_RPC: endpoint, INDEXER_WS: endpoint.replace('http:', 'ws:') };
  for (const version of ['029', '031']) {
    const contract = fileURLToPath(new URL(`./fixtures/reader-${version}.js`, import.meta.url));
    for (const blockHash of ['', `0x${hash}`]) {
      const { stdout, stderr } = await exec(process.execPath, ['--import', loader, reader, address, blockHash, contract], { env, timeout: 10000 });
      assert.equal(stderr, '');
      const state = JSON.parse(stdout);
      assert.equal(state.data.counter, 1);
      assert.equal(state.maintenanceAuthority.counter, blockHash ? 1 : 2);
      assert.deepEqual(state.operations, [{ operation: 'fixture', verifierKey: null }]);
      assert.deepEqual(requests.at(-1), { address, offset: blockHash ? { blockOffset: { hash } } : null });
    }
  }
  const { stdout } = await exec(process.execPath, ['--import', loader, reader, address, `0x${hash}`], { env, timeout: 10000 });
  assert.equal(JSON.parse(stdout).maintenanceAuthority.counter, 1);
  assert.equal('data' in JSON.parse(stdout), false);
});

test('fixed reader rejects missing or malformed state without exposing provider credentials', async t => {
  let response = { data: { contractAction: null } };
  const server = createServer((req, res) => {
    req.resume();
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify(response));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => server.close(resolve)));
  const endpoint = `http://127.0.0.1:${server.address().port}/graphql?project_id=private-test-token`;
  const env = { ...process.env, MIDNIGHT_NETWORK_ID: 'preview', INDEXER_RPC: endpoint, INDEXER_WS: endpoint.replace('http:', 'ws:') };
  for (const [data, code] of [[null, 'indexer_state_unavailable'], [{ __typename: 'ContractUpdate', state: 'deadbeef' }, 'state_decode_failed']]) {
    response = { data: { contractAction: data } };
    await assert.rejects(exec(process.execPath, ['--import', loader, reader, address, ''], { env, timeout: 10000 }), err => {
      assert.equal(err.stdout, '');
      assert.deepEqual(JSON.parse(err.stderr), { error_code: code });
      assert.doesNotMatch(err.stderr, /private-test-token|project_id|127\.0\.0\.1/);
      return true;
    });
  }
  response = { data: { contractAction: { __typename: 'ContractUpdate', state: stateFixture() } } };
  const dir = await mkdtemp(join(tmpdir(), 'midnight-reader-test-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const contract = `${dir}/future.mjs`;
  await writeFile(contract, `import * as runtime from '@midnight-ntwrk/compact-runtime'; runtime.checkRuntimeVersion('0.20.0');`);
  await assert.rejects(exec(process.execPath, ['--import', loader, reader, address, '', contract], { env, timeout: 10000 }), err => {
    assert.equal(err.stdout, '');
    assert.deepEqual(JSON.parse(err.stderr), { error_code: 'state_read_failed' });
    assert.doesNotMatch(err.stderr, /private-test-token|project_id|127\.0\.0\.1/);
    return true;
  });
});
