import { test } from 'node:test';
import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import { WebSocketServer } from 'ws';
import { readContractState, readContractSnapshot, stateProvider, stateResult, readerDiagnostic } from './state.mjs';
import { stateFixture } from './fixtures/state-fixture.mjs';

const address = 'ab'.repeat(32);
const historical = '0x' + 'cd'.repeat(32);
const finalized = '0x' + 'ef'.repeat(32);

function providerEnv(t, extra = {}) {
  const values = {
    MIDNIGHT_NETWORK_ID: 'preprod',
    RPC_NODE: 'https://node.example/rpc?token=PRIVATE_TOKEN',
    INDEXER_RPC: 'https://unused.example/graphql',
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

function reply(request, result) {
  return new Response(JSON.stringify({ jsonrpc: '2.0', id: request.id, result }));
}

function diagnostic(code, status) {
  return error => {
    assert.deepEqual(readerDiagnostic(error), {
      error_code: code,
      ...(status ? { upstream_status: status } : {}),
    });
    assert.doesNotMatch(error.message, /SECRET|PRIVATE_TOKEN|project_id|node.example/);
    return true;
  };
}

test('Preprod HTTP pins latest reads to finalized state and preserves historical hashes', async t => {
  providerEnv(t);
  const requests = [];
  const provider = stateProvider({ fetchImpl: async (url, options) => {
    assert.equal(url, process.env.RPC_NODE);
    const request = JSON.parse(options.body);
    requests.push(request);
    return reply(request, request.method === 'chain_getFinalizedHead' ? finalized
      : stateFixture(request.params[1] === historical ? 1n : 2n));
  } });
  const latest = await readContractState('0x' + address, '', provider);
  assert.equal(latest.maintenanceAuthority.counter, 2n);
  assert.deepEqual(requests.map(r => [r.method, r.params]), [
    ['chain_getFinalizedHead', []], ['midnight_contractState', [address, finalized]],
  ]);
  requests.length = 0;
  for (const hash of [historical, historical.slice(2)]) {
    const state = await readContractState(address, hash, provider);
    assert.equal(state.maintenanceAuthority.counter, 1n);
  }
  assert.deepEqual(requests.map(r => [r.method, r.params]), [
    ['midnight_contractState', [address, historical]],
    ['midnight_contractState', [address, historical]],
  ]);
  const pinned = await readContractSnapshot(address, '', provider);
  assert.deepEqual(pinned.snapshot, { block_hash: finalized });
  assert.equal(pinned.state.maintenanceAuthority.counter, 2n);
});

test('Preprod resolves explicit block heights and never falls back from missing history', async t => {
  providerEnv(t);
  const requests = [];
  const provider = stateProvider({ fetchImpl: async (_url, options) => {
    const request = JSON.parse(options.body);
    requests.push(request);
    return reply(request, request.method === 'chain_getBlockHash'
      ? (request.params[0] === 0 ? historical : null) : stateFixture());
  } });
  assert.ok(await provider.queryContractState(address, { type: 'blockHeight', blockHeight: 0 }));
  await assert.rejects(provider.queryContractState(address, { type: 'blockHeight', blockHeight: 100 }),
    diagnostic('rpc_block_not_found'));
  assert.deepEqual(requests.map(r => r.method), ['chain_getBlockHash', 'midnight_contractState', 'chain_getBlockHash']);
});

test('Preprod handles empty snapshots and rejects malformed state and JSON-RPC envelopes', async t => {
  providerEnv(t);
  for (const raw of ['', '0x']) {
    const provider = stateProvider({ fetchImpl: async (_url, options) => reply(JSON.parse(options.body), raw) });
    assert.equal(await readContractState(address, historical, provider), null);
    assert.throws(() => stateResult(null), diagnostic('state_not_found'));
  }
  for (const [value, code] of [
    [null, 'rpc_invalid_response'], [12, 'rpc_invalid_response'],
    ['xyz', 'state_decode_failed'], ['deadbeef', 'state_decode_failed'],
  ]) {
    const provider = stateProvider({ fetchImpl: async (_url, options) => reply(JSON.parse(options.body), value) });
    await assert.rejects(readContractState(address, historical, provider), diagnostic(code));
  }
  for (const [response, code] of [
    [() => [], 'rpc_invalid_response'],
    [r => ({ id: r.id, result: '' }), 'rpc_invalid_response'],
    [r => ({ jsonrpc: '2.0', id: r.id + 1, result: '' }), 'rpc_invalid_response'],
    [r => ({ jsonrpc: '2.0', id: r.id }), 'rpc_invalid_response'],
    [r => ({ jsonrpc: '2.0', id: r.id, result: '', error: {} }), 'rpc_invalid_response'],
    [r => ({ jsonrpc: '2.0', id: r.id, error: { code: -32602, message: 'RPC_SECRET', data: 'PAYLOAD_SECRET' } }), 'rpc_call_failed'],
  ]) {
    let calls = 0;
    const provider = stateProvider({ fetchImpl: async (_url, options) => {
      calls++;
      return new Response(JSON.stringify(response(JSON.parse(options.body))));
    } });
    await assert.rejects(readContractState(address, historical, provider), diagnostic(code));
    assert.equal(calls, 1);
  }
});

test('Preprod retries transient HTTP errors within one deadline and hides provider details', async t => {
  providerEnv(t);
  for (const status of [429, 503]) {
    let calls = 0;
    const provider = stateProvider({ retryDelay: 0, fetchImpl: async (_url, options) => {
      calls++;
      return calls === 1 ? new Response('BODY_SECRET', { status }) : reply(JSON.parse(options.body), '');
    } });
    assert.equal(await readContractState(address, historical, provider), null);
    assert.equal(calls, 2);
  }
  for (const status of [400, 401, 403, 410]) {
    let calls = 0;
    const provider = stateProvider({ retryDelay: 0, fetchImpl: async () => {
      calls++;
      return new Response('BODY_SECRET', { status });
    } });
    await assert.rejects(readContractState(address, historical, provider), diagnostic('rpc_http_failed', status));
    assert.equal(calls, 1);
  }
  let calls = 0;
  const provider = stateProvider({ retryDelay: 0, fetchImpl: async () => {
    calls++;
    throw new Error('https://node.example/?token=NETWORK_SECRET');
  } });
  await assert.rejects(readContractState(address, historical, provider), diagnostic('rpc_network_failed'));
  assert.equal(calls, 5);
});

test('Preprod bounds declared and streamed HTTP responses before parsing', async t => {
  providerEnv(t, { MIDNIGHT_RPC_RESPONSE_LIMIT_BYTES: '64' });
  for (const declared of [false, true]) {
    let canceled = false, calls = 0;
    const provider = stateProvider({ fetchImpl: async () => {
      calls++;
      return new Response(new ReadableStream({
        pull(controller) { controller.enqueue(new Uint8Array(128)); },
        cancel() { canceled = true; },
      }), { headers: declared ? { 'content-length': '1000' } : {} });
    } });
    await assert.rejects(readContractState(address, historical, provider), diagnostic('rpc_response_limit'));
    assert.equal(canceled, true);
    assert.equal(calls, 1);
  }
  const provider = stateProvider({ fetchImpl: async () => new Response('INVALID_JSON_SECRET') });
  await assert.rejects(readContractState(address, historical, provider), diagnostic('rpc_invalid_response'));
});

test('Preprod validates configuration and offsets before making requests', async t => {
  providerEnv(t, { RPC_NODE: 'file:///SECRET' });
  assert.throws(() => stateProvider(), diagnostic('rpc_config_invalid'));
  process.env.RPC_NODE = 'https://node.example';
  const provider = stateProvider({ fetchImpl: async () => assert.fail('invalid input reached RPC') });
  await assert.rejects(readContractState('invalid', historical, provider), diagnostic('rpc_invalid_request'));
  for (const offset of [{ type: 'blockHash', blockHash: 'bad' }, { type: 'blockHeight', blockHeight: -1 }, { type: 'unknown' }]) {
    await assert.rejects(provider.queryContractState(address, offset), diagnostic('rpc_invalid_request'));
  }
});

test('Preprod HTTP deadline interrupts retry waits', async t => {
  providerEnv(t, { MIDNIGHT_STATE_TIMEOUT_MS: '25' });
  const keepAlive = setInterval(() => {}, 1000);
  let calls = 0;
  try {
    const provider = stateProvider({ fetchImpl: async () => {
      calls++;
      return new Response('', { status: 503 });
    } });
    await assert.rejects(readContractState(address, historical, provider), diagnostic('deadline_exceeded'));
    assert.equal(calls, 1);
  } finally { clearInterval(keepAlive); }
});

async function websocketServer(t, handler) {
  const server = new WebSocketServer({ host: '127.0.0.1', port: 0 });
  await once(server, 'listening');
  t.after(async () => {
    for (const client of server.clients) client.terminate();
    await new Promise(resolve => server.close(resolve));
  });
  server.on('connection', handler);
  return `ws://127.0.0.1:${server.address().port}/rpc?token=PRIVATE_TOKEN`;
}

test('Preprod WebSocket reuses one bounded connection for finalized and state requests', async t => {
  let connections = 0, closed = 0;
  const requests = [];
  const endpoint = await websocketServer(t, (socket, request) => {
    connections++;
    assert.equal(new URL(request.url, 'http://node').searchParams.get('token'), 'PRIVATE_TOKEN');
    socket.on('close', () => closed++);
    socket.on('message', data => {
      const r = JSON.parse(data.toString());
      requests.push(r);
      socket.send(JSON.stringify({ jsonrpc: '2.0', id: r.id,
        result: r.method === 'chain_getFinalizedHead' ? finalized : stateFixture(4n) }));
    });
  });
  providerEnv(t, { RPC_NODE: endpoint });
  const provider = stateProvider();
  assert.equal((await readContractState(address, '', provider)).maintenanceAuthority.counter, 4n);
  assert.equal(connections, 1);
  assert.deepEqual(requests.map(r => [r.method, r.params]), [
    ['chain_getFinalizedHead', []], ['midnight_contractState', [address, finalized]],
  ]);
  assert.equal((await readContractState(address, historical, provider)).maintenanceAuthority.counter, 4n);
  assert.equal(connections, 2);
  assert.deepEqual(requests[2].params, [address, historical]);
  // A subsequent connection proves the earlier state request has released its socket.
  await new Promise(resolve => setImmediate(resolve));
  assert.ok(closed >= 1);
});

test('Preprod WebSocket rejects oversize frames and closes stalled requests at the deadline', async t => {
  for (const mode of ['oversize', 'stalled', 'invalid', 'rpc_error', 'closed']) {
    await t.test(mode, async t => {
      const endpoint = await websocketServer(t, socket => socket.on('message', data => {
        const r = JSON.parse(data.toString());
        if (mode === 'oversize') socket.send('x'.repeat(1024));
        if (mode === 'invalid') socket.send('JSON_SECRET');
        if (mode === 'rpc_error') socket.send(JSON.stringify({ jsonrpc: '2.0', id: r.id, error: { message: 'RPC_SECRET' } }));
        if (mode === 'closed') socket.close();
      }));
      providerEnv(t, { RPC_NODE: endpoint, MIDNIGHT_RPC_RESPONSE_LIMIT_BYTES: '128', MIDNIGHT_STATE_TIMEOUT_MS: '250' });
      const codes = { oversize: 'rpc_response_limit', stalled: 'deadline_exceeded', invalid: 'rpc_invalid_response', rpc_error: 'rpc_call_failed', closed: 'rpc_network_failed' };
      await assert.rejects(readContractState(address, historical), diagnostic(codes[mode]));
    });
  }
});

test('Preprod WebSocket handshake failure preserves only the HTTP status', async t => {
  const server = createServer((_req, res) => res.writeHead(403).end('HANDSHAKE_SECRET'));
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => server.close(resolve)));
  providerEnv(t, { RPC_NODE: `ws://127.0.0.1:${server.address().port}` });
  await assert.rejects(readContractState(address, historical), diagnostic('rpc_http_failed', 403));
});

// The Indexer has no action at an idle block, but the contract snapshot exists.
test('Mainnet reads Ledger 8 and Ledger 9 snapshots without consulting the Indexer', async t => {
 providerEnv(t, { MIDNIGHT_NETWORK_ID: 'mainnet', INDEXER_RPC: 'unused' });
 const { readFile } = await import('node:fs/promises');
 const fixture = JSON.parse(await readFile(new URL('./fixtures/stagenet-state-v8.json', import.meta.url)));
 const requests = [];
 const provider = stateProvider({ fetchImpl: async (url, options) => {
  assert.equal(url, process.env.RPC_NODE);
  const r = JSON.parse(options.body); requests.push(r);
  return reply(r, r.method === 'chain_getFinalizedHead' ? finalized : r.params[1] === historical ? stateFixture(1n) : fixture.state ?? fixture.State);
 } });
 assert.equal((await readContractState(address, historical, provider)).maintenanceAuthority.counter, 1n);
 assert.ok(stateResult(await readContractState(address, '', provider)).operations.length > 0);
 assert.deepEqual(requests.map(r => r.method), ['midnight_contractState', 'chain_getFinalizedHead', 'midnight_contractState']);
});
