import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { once } from 'node:events';
import { cp, mkdir, mkdtemp, rm } from 'node:fs/promises';
import { createServer } from 'node:http';
import path from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import { WebSocketServer } from 'ws';
import { stateFixture } from '../static/fixtures/state-fixture.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const address = 'ab'.repeat(32);
const historical = '0x' + 'cd'.repeat(32);
const finalized = '0x' + 'ef'.repeat(32);

async function listen(server) {
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  return server.address().port;
}

async function within(promise, ms, message) {
  let timer;
  try {
    return await Promise.race([promise, new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error(message)), ms);
    })]);
  } finally { clearTimeout(timer); }
}

async function startService(t, endpoint, verified = false, network = 'preprod') {
  const work = await mkdtemp(path.join(root, '.e2e-preprod-rpc-'));
  const staticDir = path.join(work, 'static');
  await mkdir(staticDir);
  for (const asset of ['state.mjs', 'read-state.mjs', 'register-runtime.mjs', 'runtime-loader.mjs']) {
    await cp(path.join(root, 'static', asset), path.join(staticDir, asset));
  }
  if (verified) {
    const dir = path.join(staticDir, 'verified', address, 'src', 'managed', 'contract');
    await mkdir(dir, { recursive: true });
    await cp(path.join(root, 'static', 'fixtures', 'reader-029.js'), path.join(dir, 'index.js'));
  }
  const reservation = createServer();
  const port = await listen(reservation);
  await new Promise(resolve => reservation.close(resolve));
  const service = spawn(path.join(root, 'e2e', '.midnight-server'), [], {
    cwd: root,
    env: {
      ...process.env, NETWORK_ID: 'preview', PORT: String(port), STATIC_DIR: staticDir,
      TEMP_DIR: path.join(work, 'tmp'), [`MIDNIGHT_${network.toUpperCase()}_RPC_NODE`]: endpoint || '',
      // A functioning RPC read must never contact these invalid Indexer settings.
      [`MIDNIGHT_${network.toUpperCase()}_INDEXER_RPC`]: 'unused', [`MIDNIGHT_${network.toUpperCase()}_INDEXER_WS`]: 'unused',
      BLOCKFROST_PROJECT_ID: '',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let logs = '', errors = '';
  service.stdout.on('data', chunk => { logs = (logs + chunk).slice(-65536); });
  service.stderr.on('data', chunk => { errors = (errors + chunk).slice(-4096); });
  t.after(async () => {
    try {
      if (service.exitCode === null && service.signalCode === null) {
        const closed = once(service, 'close');
        service.kill('SIGTERM');
        try { await within(closed, 17000, 'helper shutdown timed out'); }
        catch { service.kill('SIGKILL'); await closed; }
      }
    } finally { await rm(work, { recursive: true, force: true }); }
  });
  const base = `http://127.0.0.1:${port}/contract/state`;
  let ready = false;
  for (let attempt = 0; attempt < 50; attempt++) {
    assert.equal(service.exitCode, null, errors);
    try {
      const response = await fetch(base, { signal: AbortSignal.timeout(500) });
      await response.text();
      if (response.status === 400) { ready = true; break; }
    } catch { /* The helper has not bound its port yet. */ }
    await delay(50);
  }
  assert.ok(ready, 'helper did not start');
  return {
    logs: () => logs,
    async read(params, options = {}) {
      return fetch(base + '?' + new URLSearchParams(params), options);
    },
  };
}

for (const network of ['preprod', 'mainnet']) {
for (const transport of ['http', 'ws']) {
  test(`${network} ${transport} preserves the real helper HTTP contract and history`, { timeout: 30000 }, async t => {
    const requests = [];
    let stalled, disconnected;
    const requested = new Promise(resolve => { stalled = resolve; });
    const closed = new Promise(resolve => { disconnected = resolve; });
    let mode = 'normal';
    function handle(request, send, onClose) {
      requests.push(request);
      if (mode === 'stalled') { onClose(disconnected); stalled(); return; }
      if (mode === 'error') {
        send({ jsonrpc: '2.0', id: request.id, error: { code: -32000, message: 'PROVIDER_SECRET' } });
        return;
      }
      const result = request.method === 'chain_getFinalizedHead' ? finalized
        : request.params[1] === '0x' + '00'.repeat(32) ? ''
          : stateFixture(request.params[1] === historical ? 1n : 2n);
      send({ jsonrpc: '2.0', id: request.id, result });
    }
    let endpoint;
    if (transport === 'http') {
      const server = createServer(async (req, res) => {
        let body = '';
        for await (const chunk of req) body += chunk;
        handle(JSON.parse(body), value => {
          res.setHeader('Content-Type', 'application/json');
          res.end(JSON.stringify(value));
        }, callback => res.once('close', callback));
      });
      endpoint = `http://127.0.0.1:${await listen(server)}/rpc?token=RPC_PRIVATE_TOKEN`;
      t.after(async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); });
    } else {
      const server = new WebSocketServer({ host: '127.0.0.1', port: 0 });
      await once(server, 'listening');
      server.on('connection', socket => socket.on('message', data => {
        handle(JSON.parse(data.toString()), value => socket.send(JSON.stringify(value)), callback => socket.once('close', callback));
      }));
      endpoint = `ws://127.0.0.1:${server.address().port}/rpc?token=RPC_PRIVATE_TOKEN`;
      t.after(async () => {
        for (const socket of server.clients) socket.terminate();
        await new Promise(resolve => server.close(resolve));
      });
    }
    const helper = await startService(t, endpoint, true, network);
    const baseParams = { contract_addr: address, network_id: network };
    for (const [params, counter, ledger] of [
      [baseParams, 2, true],
      [{ ...baseParams, block_hash: historical }, 1, true],
      [{ contract_addr: address, networkid: network, hash: historical, view: 'cma' }, 1, false],
      [{ ...baseParams, hash: finalized, block_hash: historical }, 1, true],
    ]) {
      const response = await helper.read(params);
      assert.equal(response.status, 200);
      const state = await response.json();
      assert.deepEqual(state.maintenanceAuthority, { committee: [], threshold: 0, counter });
      assert.deepEqual(state.operations, [{ operation: 'fixture', verifierKey: '' }]);
      if (ledger) assert.deepEqual(state.data, { counter: 1 });
      else assert.equal('data' in state, false);
    }
    assert.deepEqual(requests.map(r => [r.method, r.params]), [
      ['chain_getFinalizedHead', []], ['midnight_contractState', [address, finalized]],
      ['midnight_contractState', [address, historical]],
      ['midnight_contractState', [address, historical]],
      ['midnight_contractState', [address, historical]],
    ]);
    const empty = await helper.read({ ...baseParams, block_hash: '0x' + '00'.repeat(32) });
    assert.equal(empty.status, 404);
    await empty.text();
    assert.equal(requests.at(-1).params[1], '0x' + '00'.repeat(32));
    mode = 'error';
    const failure = await helper.read({ ...baseParams, block_hash: historical });
    assert.equal(failure.status, 502);
    assert.doesNotMatch(await failure.text(), /PROVIDER_SECRET|RPC_PRIVATE_TOKEN/);
    mode = 'stalled';
    const controller = new AbortController();
    const read = helper.read({ ...baseParams, block_hash: historical }, { signal: controller.signal })
      .then(() => assert.fail('stalled RPC returned'), error => error);
    await within(requested, 5000, 'reader never reached RPC');
    controller.abort();
    assert.equal((await read).name, 'AbortError');
    await within(closed, 3000, 'canceled reader kept its RPC connection open');
    assert.doesNotMatch(helper.logs(), /PROVIDER_SECRET|RPC_PRIVATE_TOKEN|project_id|\/rpc\?/);
    assert.match(helper.logs(), /rpc_call_failed/);
    assert.match(helper.logs(), /state_not_found/);
  });
}

}

// Opt-in read-only acceptance uses the default Blockfrost RPC with a Preprod
// project ID, or an existing custom-node override. Credentials stay in the env.
test('Preprod configured RPC acceptance through the real helper', {
  skip: process.env.MIDNIGHT_PREPROD_LIVE !== '1', timeout: 90000,
}, async t => {
  const endpoint = process.env.MIDNIGHT_PREPROD_RPC_NODE?.trim();
  assert.ok(endpoint || process.env.MIDNIGHT_PREPROD_BLOCKFROST_PROJECT_ID?.trim(),
    'MIDNIGHT_PREPROD_BLOCKFROST_PROJECT_ID is required for live acceptance unless a custom RPC override is supplied');
  const helper = await startService(t, endpoint);
  const contract = 'b2254962147c9cc2e5b9fbcf39f8a4e0d1f149b7801a651df2e41dda1e4410d9';
  for (const [hash, counter, operations] of [
    ['0xf81b0f63fa5009acb50ca9a8505087208649e44e1d68787f384f9878e13337b2', 0, 1],
    ['0xbcafab53db30b9f6c2cade7ebc367c7d8946e9b46dcbe19bceae832ed3cb3749', 1, 2],
    ['0x6e5902c2742c7b90232227c6ae6556074127ed192601882f41bd111f5654ad22', 1, 2],
  ]) {
    const response = await helper.read({ contract_addr: contract, network_id: 'preprod', block_hash: hash });
    assert.equal(response.status, 200);
    const state = await response.json();
    assert.equal(state.maintenanceAuthority.counter, counter);
    assert.equal(state.operations.length, operations);
    assert.ok(state.operations.every(o => o.verifierKey?.length === 2119 * 2));
  }
  const old = await helper.read({ contract_addr: '5fe015ddbbb3bbec18a5bb8cde94702b919870102a77092dca894bdd37b83505', network_id: 'preprod',
    block_hash: '0xe4ba0610dc717e9e08eca52330bf7465695249d5379b75eb58d8207d5465c7fc' });
  assert.equal(old.status, 200);
  const state = await old.json();
  assert.deepEqual(state.maintenanceAuthority.committee, ['e7f20aae35bc7d766173de928ea7fdf00f30a61df4642dcb83de7d2532ff1685']);
  const keys = Object.fromEntries(state.operations.map(o => [o.operation,
    createHash('md5').update(Buffer.from(o.verifierKey, 'hex')).digest('hex')]));
  assert.deepEqual(keys, {
    acceptTrade: 'a3cb69bedbb61e6061a75143a9ae2415', cancelListing: 'a3cb69bedbb61e6061a75143a9ae2415',
    createListing: '60ef3ca1c9b15daefa0b37d569288ce1', getListing: 'd84148c852117efc7168b885b34fe22c',
    getListingCount: '44141c6b08739bd3d97aad6a801449da',
  });
  const deployed = await helper.read({ contract_addr: 'a28909dccf5e67984e93195c2bce8f6207f2f0625d7343f7f7d64398389e9267', network_id: 'preprod',
    block_hash: '0x77a827c1aaf6a1a7cb02c93fe7ec14a97409f9c5176c7fdb8dc37ab60651726a' });
  assert.equal(deployed.status, 200);
  assert.equal((await deployed.json()).operations.length, 4);
  const beforeDeploy = await helper.read({ contract_addr: 'a28909dccf5e67984e93195c2bce8f6207f2f0625d7343f7f7d64398389e9267', network_id: 'preprod',
    block_hash: '0x5c1bc1ab6ba3be85fa01c900e172459b8f7d8f956a55db04ef1fb3872f37489a' });
  assert.equal(beforeDeploy.status, 404);
  await beforeDeploy.text();
  const latest = await helper.read({ contract_addr: contract, network_id: 'preprod' });
  assert.equal(latest.status, 200);
  assert.ok((await latest.json()).maintenanceAuthority.counter >= 1);
});
