import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readContractState, stateResult, replacer, readerDiagnostic } from './state.mjs';
import { indexerPublicDataProvider } from '@midnight-ntwrk/midnight-js-indexer-public-data-provider';
import { setNetworkId } from '@midnight-ntwrk/midnight-js-network-id';
import { compiledLedger } from './state.mjs';
import * as currentRuntime from '@midnight-ntwrk/compact-runtime';
import * as legacyRuntime from '@midnight-ntwrk/compact-runtime-0.14';
import * as legacyContract from './fixtures/reader-029.js';
import * as currentContract from './fixtures/reader-031.js';

test('real Compact 0.29 and 0.31.1 readers survive SDK runtime upgrades', async () => {
  for (const [contract, runtime, fixture] of [
    [legacyContract, legacyRuntime, '029'],
    [currentContract, currentRuntime, '031'],
  ]) {
    const original = new contract.Contract({}).initialState(
      runtime.createConstructorContext({}, '00'.repeat(32)),
    ).currentContractState.data;
    const value =
      typeof original.encode === 'function' ? original : original.state;
    const upgraded = currentRuntime.StateValue.decode(value.encode());
    const ledger = await compiledLedger(
      new URL(`./fixtures/reader-${fixture}.js`, import.meta.url),
      contract.ledger,
    );
    assert.equal(ledger(upgraded).counter, 1n);
    assert.equal(contract.ledger(original).counter, 1n);
  }
});

test('historical query sends a block hash through the real GraphQL provider', async (t) => {
  let request;
  const server = createServer(async (req, res) => {
    let body = '';
    for await (const chunk of req) body += chunk;
    request = JSON.parse(body);
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ data: { contractAction: null } }));
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => server.close(resolve)));
  const url = `http://127.0.0.1:${server.address().port}/graphql?project_id=test-token`;
  setNetworkId('preview');
  const provider = indexerPublicDataProvider(url, url.replace('http:', 'ws:'));
  assert.equal(
    await readContractState('ab'.repeat(32), '0x' + 'cd'.repeat(32), provider),
    null,
  );
  assert.deepEqual(request.variables.offset, {
    blockOffset: { hash: 'cd'.repeat(32) },
  });
  assert.equal(request.variables.address, 'ab'.repeat(32));
  await readContractState('ab'.repeat(32), '', provider);
  assert.equal(request.variables.offset, null);
});

test('state errors do not become empty successful responses or rounded amounts', () => {
  assert.throws(() => stateResult(null), /not found/);
  assert.equal(
    JSON.stringify({ n: 9007199254740993n }, replacer),
    '{"n":"9007199254740993"}',
  );
  assert.equal(JSON.stringify({ n: 1n }, replacer), '{"n":1}');
  const state = {
    maintenanceAuthority: { committee: ['key'], threshold: 1, counter: 2n },
    operations: () => [],
  };
  assert.deepEqual(
    JSON.parse(JSON.stringify(stateResult(state), replacer))
      .maintenanceAuthority,
    { committee: ['key'], threshold: 1, counter: 2 },
  );
  state.maintenanceAuthority.counter = 9007199254740993n;
  assert.throws(() => stateResult(state), /counter/);
});

function providerEnv(t, extra = {}) {
  const values = {
    MIDNIGHT_NETWORK_ID: 'preview',
    INDEXER_RPC: 'http://127.0.0.1/graphql?project_id=private-token',
    ...extra,
  };
  for (const [key, value] of Object.entries(values)) {
    const original = process.env[key];
    process.env[key] = value;
    t.after(() => {
      if (original === undefined) delete process.env[key];
      else process.env[key] = original;
    });
  }
}

test('HTTP-only reader preserves historical offsets and provider credentials', async (t) => {
  providerEnv(t);
  const { stateProvider } = await import('./state.mjs');
  const requests = [];
  const provider = stateProvider({
    fetchImpl: async (url, options) => {
      assert.equal(url, process.env.INDEXER_RPC);
      requests.push(JSON.parse(options.body));
      return new Response(JSON.stringify({ data: { contractAction: null } }));
    },
  });
  await assert.rejects(
    readContractState('ab'.repeat(32), '0x' + 'cd'.repeat(32), provider),
    /Indexer has no contract action/,
  );
  assert.deepEqual(requests[0].variables, {
    address: 'ab'.repeat(32),
    offset: { blockOffset: { hash: 'cd'.repeat(32) } },
  });
  await assert.rejects(readContractState('ab'.repeat(32), '', provider), /Indexer has no contract action/);
  assert.equal(requests[1].variables.offset, null);
});

test('HTTP-only reader retries transient failures but rejects GraphQL and client errors immediately', async (t) => {
  providerEnv(t);
  const { stateProvider } = await import('./state.mjs');
  for (const status of [429, 503]) {
    let calls = 0;
    const provider = stateProvider({
      retryDelay: 0,
      fetchImpl: async () => {
        calls++;
        return calls === 1
          ? new Response('temporary', { status })
          : new Response('{"data":{"contractAction":null}}');
      },
    });
    await assert.rejects(provider.queryContractState('ab'.repeat(32)), /Indexer has no contract action/);
    assert.equal(calls, 2);
  }
  for (const response of [
    () => new Response('bad request', { status: 400 }),
    () =>
      new Response(
        '{"errors":[{"message":"private-token"}],"data":{"contractAction":null}}',
      ),
  ]) {
    let calls = 0;
    const provider = stateProvider({
      retryDelay: 0,
      fetchImpl: async () => {
        calls++;
        return response();
      },
    });
    await assert.rejects(
      provider.queryContractState('ab'.repeat(32)),
      (error) => !error.message.includes('private-token'),
    );
    assert.equal(calls, 1);
  }
  let calls = 0;
  const provider = stateProvider({
    retryDelay: 0,
    fetchImpl: async () => {
      calls++;
      throw new Error('network unavailable');
    },
  });
  await assert.rejects(
    provider.queryContractState('ab'.repeat(32)),
    /network unavailable/,
  );
  assert.equal(calls, 5);
});

test('HTTP-only reader caps declared and streamed responses without retrying oversize data', async (t) => {
  providerEnv(t, { MIDNIGHT_INDEXER_RESPONSE_LIMIT_BYTES: '32' });
  const { stateProvider } = await import('./state.mjs');
  for (const declared of [true, false]) {
    let calls = 0,
      canceled = false;
    const provider = stateProvider({
      retryDelay: 0,
      fetchImpl: async () => {
        calls++;
        const body = new ReadableStream({
          pull(controller) {
            controller.enqueue(new Uint8Array(64));
          },
          cancel() {
            canceled = true;
          },
        });
        return new Response(body, {
          headers: declared ? { 'content-length': '1000' } : {},
        });
      },
    });
    await assert.rejects(
      provider.queryContractState('ab'.repeat(32)),
      /exceeds configured limit/,
    );
    assert.equal(calls, 1);
    assert.equal(canceled, true);
  }
});

test('HTTP-only reader timeout also interrupts retry backoff', async (t) => {
  providerEnv(t, { MIDNIGHT_STATE_TIMEOUT_MS: '25' });
  const { stateProvider } = await import('./state.mjs');
  let calls = 0;
  const provider = stateProvider({
    retryDelay: 1000,
    fetchImpl: async () => {
      calls++;
      return new Response('', { status: 503 });
    },
  });
  const start = Date.now();
  // Keep the test process alive: AbortSignal.timeout and timer promises may be unref'ed.
  const keepAlive = setInterval(() => {}, 1000);
  try {
    await assert.rejects(provider.queryContractState('ab'.repeat(32)), {
      name: 'AbortError',
    });
  } finally {
    clearInterval(keepAlive);
  }
  assert.equal(calls, 1);
  assert.ok(Date.now() - start < 500);
});

test('reader diagnostics classify failures without provider data or credentials', async (t) => {
  providerEnv(t);
  const { stateProvider, stringifyState } = await import('./state.mjs');
  for (const [response, code, status] of [
    [() => new Response('BODY_SECRET', { status: 401 }), 'indexer_http_failed', 401],
    [() => new Response('{"errors":[{"message":"GRAPHQL_SECRET"}]}'), 'indexer_graphql_failed'],
    [() => new Response('INVALID_JSON_SECRET'), 'indexer_invalid_response'],
    [() => new Response('{"data":{"contractAction":{"state":"invalid"}}}'), 'state_decode_failed'],
  ]) {
    const provider = stateProvider({ fetchImpl: async () => response() });
    let diagnostic;
    try {
      await provider.queryContractState('ab'.repeat(32));
      assert.fail('reader unexpectedly succeeded');
    } catch (error) {
      diagnostic = readerDiagnostic(error);
    }
    assert.deepEqual(diagnostic, { error_code: code, ...(status ? { upstream_status: status } : {}) });
    assert.doesNotMatch(JSON.stringify(diagnostic), /SECRET|project_id/);
  }
  const provider = stateProvider({ retryDelay: 0, fetchImpl: async () => {
    throw new Error('https://provider.test/?project_id=NETWORK_SECRET');
  } });
  await assert.rejects(provider.queryContractState('ab'.repeat(32)), error => {
    assert.deepEqual(readerDiagnostic(error), { error_code: 'indexer_network_failed' });
    return true;
  });
  for (const [action, code] of [
    [() => stateResult(null), 'state_not_found'],
    [() => stringifyState({ data: 'x'.repeat(64) }, 8), 'state_output_limit'],
  ]) {
    assert.throws(action, error => {
      assert.deepEqual(readerDiagnostic(error), { error_code: code });
      return true;
    });
  }
  assert.deepEqual(readerDiagnostic(new Error('RAW_SECRET')), { error_code: 'state_read_failed' });
  assert.deepEqual(readerDiagnostic({ diagnosticCode: 'RAW_SECRET', upstreamStatus: 'URL_SECRET' }), { error_code: 'state_read_failed' });
});

test('state JSON size accounting matches escaping, arrays, maps and omitted properties', async () => {
  const { stringifyState } = await import('./state.mjs');
  const shared = { value: 'same object' };
  const fixtures = [
    {
      text: '中文\n"\\😀\ud800',
      empty: {},
      array: [undefined, null, true, NaN, 2n],
      omit: undefined,
      map: new Map([['a', new Set([1, 2])]]),
    },
    { first: shared, second: shared },
    [],
    { a: { b: 1 }, c: [{}, ['x', 'y']] },
    { omit: () => {}, symbol: Symbol('s'), zero: 0 },
  ];
  for (const value of fixtures) {
    const expected = JSON.stringify(value, replacer),
      bytes = Buffer.byteLength(expected);
    assert.equal(stringifyState(value, bytes), expected);
    assert.throws(
      () => stringifyState(value, bytes - 1),
      /exceeds configured limit/,
    );
  }
  assert.throws(
    () => stringifyState({ data: 'x'.repeat(1024 * 1024) }, 32),
    /exceeds configured limit/,
  );
});


test('Ledger 8 and real Stagenet Ledger 9 state dispatch by envelope', async () => {
  const { readFile } = await import('node:fs/promises');
  const { decodeContractState } = await import('./state.mjs');
  const { stateFixture } = await import('./fixtures/state-fixture.mjs');
  const fixture = JSON.parse(await readFile(new URL('./fixtures/stagenet-state-v8.json', import.meta.url), 'utf8'));
  const old = decodeContractState(Buffer.from(stateFixture(), 'hex'));
  assert.equal(old.maintenanceAuthority.counter, 2n);
  const modern = decodeContractState(Buffer.from(fixture.state, 'hex'));
  const result = stateResult(modern);
  assert.equal(result.maintenanceAuthority.committee[0].tag, 'schnorr');
  assert.ok(result.operations.length > 0);
  assert.throws(() => currentRuntime.ContractState.deserialize(Buffer.from(fixture.state, 'hex')));
  assert.throws(() => decodeContractState(Buffer.from('midnight:contract-state[v99]:')));
  assert.throws(() => decodeContractState(Buffer.from(fixture.state.slice(0, -20), 'hex')));
  // StateValue conversions between WASM eras must be verified independently.
  const { StateValue } = await import('@midnight-ntwrk/compact-runtime-0.21');
  const original = new currentContract.Contract({}).initialState(currentRuntime.createConstructorContext({}, '00'.repeat(32))).currentContractState.data;
  const value = typeof original.encode === 'function' ? original : original.state;
  const upgraded = StateValue.decode(value.encode());
  const ledger = await compiledLedger(new URL('./fixtures/reader-031.js', import.meta.url), currentContract.ledger);
  assert.equal(ledger(upgraded).counter, 1n);
});

test('real 0.35 compiler readers work in both ZKIR modes alongside historical readers', async () => {
  const modernRuntime = await import('@midnight-ntwrk/compact-runtime-0.20');
  const { readFile } = await import('node:fs/promises');
  const fixture = JSON.parse(await readFile(new URL('./fixtures/stagenet-state-v8.json', import.meta.url), 'utf8'));
  const modernState = modernRuntime.ContractState.deserialize(Buffer.from(fixture.state, 'hex'));
  assert.equal(modernState.maintenanceAuthority.committee[0].tag, 'schnorr');
  for (const mode of ['v2', 'v3']) {
    const moduleURL = new URL(`./fixtures/reader-035-${mode}.js`, import.meta.url);
    const contract = await import(moduleURL);
    const state = (await new contract.Contract({}).initialState(modernRuntime.createConstructorContext({}, '00'.repeat(32)))).currentContractState;
    const ledger = await compiledLedger(moduleURL, contract.ledger);
    assert.equal(ledger(state.data).counter, 1n);
    const value = typeof state.data.encode === 'function' ? state.data : state.data.state;
    assert.equal(currentContract.ledger(currentRuntime.StateValue.decode(value.encode())).counter, 1n);
    const old = new currentContract.Contract({}).initialState(currentRuntime.createConstructorContext({}, '00'.repeat(32))).currentContractState;
    assert.equal(ledger(old.data).counter, 1n);
  }
});


test('Compact 0.36 reader preserves current and historical contract state values', async () => {
 const runtime = await import('@midnight-ntwrk/compact-runtime-0.21');
 const { decodeContractState } = await import('./state.mjs');
 const moduleURL = new URL('./fixtures/reader-036.js', import.meta.url);
 const contract = await import(moduleURL);
 const state = (await new contract.Contract({}).initialState(runtime.createConstructorContext({}, '00'.repeat(32)))).currentContractState;
 const decoded = decodeContractState(state.serialize());
 const ledger = await compiledLedger(moduleURL, contract.ledger);
 assert.equal(ledger(decoded.data).counter, 1n);
 const historicalLedger = await compiledLedger(new URL('./fixtures/reader-031.js', import.meta.url), currentContract.ledger);
 assert.equal(historicalLedger(decoded.data).counter, 1n);
 const old = new currentContract.Contract({}).initialState(currentRuntime.createConstructorContext({}, '00'.repeat(32))).currentContractState;
 assert.equal(ledger(old.data).counter, 1n);
});
