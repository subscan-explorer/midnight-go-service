import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, readFile, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { publicArgument, readCompilerInfo } from './public-arguments.mjs';
import { explore, preparePublicCall, publicStateChanges } from './explorer.mjs';
import { merkleRoots } from './merkle.mjs';
import { proofPresence, transactionProof, deploymentBytecode } from './transaction-proof.mjs';

test('proof inspection deserializes native transactions and rejects a different snapshot', async t => {
  const { Transaction } = await import('@midnight-ntwrk/midnight-js-protocol/v9');
  // An empty native transaction has no proofs; mockProve changes its phase only.
  // This fixture is never submitted or presented as a cryptographically valid proof.
  const raw = Buffer.from(Transaction.fromParts('preview').mockProve().serialize()).toString('hex');
  const hash = 'ab'.repeat(32);
  const server = createServer(async (req, res) => {
    let body = ''; for await (const chunk of req) body += chunk;
    const query = JSON.parse(body);
    assert.ok(query.variables.offset.hash);
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ data: { transactions: [{ hash, raw, protocolVersion: 2000000 }] } }));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const previous = process.env.INDEXER_RPC;
  process.env.INDEXER_RPC = `http://127.0.0.1:${server.address().port}`;
  t.after(() => {
    if (previous === undefined) delete process.env.INDEXER_RPC; else process.env.INDEXER_RPC = previous;
    server.closeAllConnections(); server.close();
  });
  assert.deepEqual(await transactionProof('0x' + hash), {
    has_zk_proof: false, proof_count: { contract: 0, shielded: 0, dust: 0 }, transaction_hash: '0x' + hash, era: 'v9',
  });
  await assert.rejects(transactionProof('0x' + 'cd'.repeat(32)), /transaction_snapshot_unavailable/);
});

test('proof presence counts actual proof markers and ignores proof preimages', () => {
  const proof = { instance: 'proof' };
  const result = proofPresence({ intents: new Map([[1, { actions: [{ proof }, { proof: { instance: 'pre-proof' } }], dustActions: { spends: [{ proof }] } }]]),
    guaranteedOffer: { inputs: [{ proof }], outputs: [], transients: [{ inputProof: proof, outputProof: proof }] } });
  assert.deepEqual(result, { has_zk_proof: true, proof_count: { contract: 1, shielded: 3, dust: 1 } });
  assert.equal(proofPresence({}).has_zk_proof, false);
});

test('public argument conversion preserves integers and rejects ambiguous or unsupported inputs', () => {
  assert.equal(publicArgument({ 'type-name': 'Field' }, '18446744073709551615'), 18446744073709551615n);
  assert.throws(() => publicArgument({ 'type-name': 'Field' }, Number.MAX_SAFE_INTEGER + 1));
  assert.throws(() => publicArgument({ 'type-name': 'Bytes', length: 32 }, 'ab'));
  assert.throws(() => publicArgument({ 'type-name': 'Opaque', tsType: 'Function' }, '() => 1'));
  assert.deepEqual(publicArgument({ 'type-name': 'Tuple', types: [{ 'type-name': 'Boolean' }, { 'type-name': 'Bytes', length: 1 }] }, [false, 'ff']), [false, Uint8Array.of(255)]);
  assert.throws(() => publicArgument({ 'type-name': 'Field' }, '-1'));
  assert.throws(() => publicArgument({ 'type-name': 'Unknown' }, {}));
});

test('state differences retain false, zero, deletion and full precision', () => {
  assert.deepEqual(publicStateChanges({ a: 0n, b: true, removed: 'x' }, { a: 0n, b: false, added: 18446744073709551615n }), [
    { field: 'added', before: null, after: 18446744073709551615n, kind: 'added' },
    { field: 'b', before: true, after: false, kind: 'changed' },
    { field: 'removed', before: 'x', after: null, kind: 'removed' },
  ]);
});

test('real Compact artifacts build a ledger 9 call and check native Merkle paths and exact transaction snapshots', async t => {
  const exec = promisify(execFile);
  let installed;
  try { installed = await exec('compact', ['list']); } catch { t.skip('Compact 0.36.0 required'); return; }
  if (!installed.stdout.includes('0.36.0')) { t.skip('Compact 0.36.0 required'); return; }
  const dir = await mkdtemp(join(tmpdir(), 'midnight-explorer-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  await exec('compact', ['compile', '+0.36.0', new URL('./fixtures/explorer.compact', import.meta.url).pathname, dir]);
  const runtime = await import('@midnight-ntwrk/compact-runtime-0.21');
  const ledger = await import('@midnight-ntwrk/midnight-js-protocol/v9');
  const module = await import(pathToFileURL(join(dir, 'contract/index.js')).href);
  const contract = new module.Contract({ privateValue: () => { throw new Error('private_witness_required'); } });
  const state = (await contract.initialState(runtime.createConstructorContext(undefined, '00'.repeat(32)))).currentContractState;
  for (const circuit of ['set', 'insert', 'needsWitness', 'insertComposite']) {
    const op = state.operation(circuit); op.verifierKey = await readFile(join(dir, `keys/${circuit}.verifier`)); state.setOperation(circuit, op);
  }
  const deployedState = ledger.ContractState.deserialize(state.serialize());
  const deployment = new ledger.ContractDeploy(deployedState);
  const other = new ledger.ContractDeploy(new ledger.ContractState());
  const intent = ledger.Intent.new(new Date(Date.now() + 300000)).addDeploy(other).addDeploy(deployment);
  const rawDeployment = Buffer.from(ledger.Transaction.fromParts('preview', undefined, undefined, intent).mockProve().serialize()).toString('hex');
  const context = runtime.createCircuitContext({ circuitId: 'insert', contractAddress: 'ab'.repeat(32), coinPublicKeyOrZswapState: '00'.repeat(32), contractState: state.data, privateState: undefined });
  const inserted = await contract.circuits.insert(context, 42n);
  state.data = new runtime.ChargedState(inserted.context.callContext.currentQueryContext.state.state);
  const info = await readCompilerInfo(dir);
  const composite = { owner: '0xabcd', amount: '18446744073709551615', active: false, optional: { is_some: true, value: '42' }, pair: ['0', '7'], choice: 1 };
  const compositeArgs = [composite, [true, '255'], '18446744073709551615', 1];
  const compositeInfo = info.circuits.find(c => c.name === 'insertComposite');
  const compositeContext = runtime.createCircuitContext({ circuitId: 'insertComposite', contractAddress: 'ab'.repeat(32), coinPublicKeyOrZswapState: '00'.repeat(32), contractState: state.data, privateState: undefined });
  const compositeInserted = await contract.circuits.insertComposite(compositeContext, ...compositeInfo.arguments.map((arg, i) => publicArgument(arg.type, compositeArgs[i])));
  state.data = new runtime.ChargedState(compositeInserted.context.callContext.currentQueryContext.state.state);
  const roots = merkleRoots(module.ledger(state.data.state), info, runtime);
  assert.equal(roots[0].field, 'commitments');
  const stateHex = Buffer.from(state.serialize()).toString('hex');
  const txHash = 'cd'.repeat(32);
  const requests = [];
  const server = createServer(async (req, res) => {
    let body = ''; for await (const chunk of req) body += chunk;
    const q = JSON.parse(body); requests.push(q);
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ data: {
      block: { hash: 'ab'.repeat(32), height: 200, protocolVersion: 2000000,
        ledgerParameters: Buffer.from(ledger.LedgerParameters.initialParameters().serialize()).toString('hex'),
        contractZswapState: Buffer.from(new ledger.ZswapChainState().serialize()).toString('hex') },
      contract: { state: stateHex }, contractAction: { state: stateHex, transaction: { hash: txHash, block: { hash: "ab".repeat(32), height: 200 } } },
      transactions: [{ hash: txHash, raw: rawDeployment, protocolVersion: 2000000 }],
    } }));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => { server.closeAllConnections(); server.close(); });
  const names = ['INDEXER_RPC', 'INDEXER_WS', 'MIDNIGHT_NETWORK_ID'];
  const old = names.map(name => process.env[name]);
  t.after(() => names.forEach((name, i) => old[i] === undefined ? delete process.env[name] : process.env[name] = old[i]));
  process.env.INDEXER_RPC = `http://127.0.0.1:${server.address().port}`;
  process.env.INDEXER_WS = process.env.INDEXER_RPC.replace('http:', 'ws:');
  process.env.MIDNIGHT_NETWORK_ID = 'preview';
  const deploymentResult = await explore({ operation: 'deployment', contract: '0x' + deployment.address, transaction_hash: '0x' + txHash }, '/no-verified-artifacts');
  assert.equal(deploymentResult.source, 'contract_deploy');
  assert.equal(deploymentResult.action_index, 1);
  assert.equal(deploymentResult.transaction_bytes, rawDeployment);
  assert.equal(deploymentResult.operations.length, 4);
  for (const operation of deploymentResult.operations) {
    assert.equal(operation.bytecode, Buffer.from(deployedState.operation(operation.name).serialize()).toString('hex'));
    assert.notEqual(operation.bytecode, stateHex, 'deployment code must never be a state snapshot');
    assert.equal(operation.byte_length, operation.bytecode.length / 2);
  }
  await assert.rejects(deploymentBytecode('0x' + txHash, '0x' + 'ef'.repeat(32)), /deployment_not_found/);
  await assert.rejects(deploymentBytecode('0x' + 'ef'.repeat(32), '0x' + deployment.address), /transaction_snapshot_unavailable/);
  assert.deepEqual((await deploymentBytecode('0x' + txHash, '0x' + other.address)).operations, []);
  const request = { contract: '0x' + 'ab'.repeat(32), circuit: 'set', arguments: ['42'], coin_public_key: '00'.repeat(32), encryption_public_key: '00'.repeat(32) };
  const built = await preparePublicCall(request, dir);
  assert.equal(built.era, 'v9');
  assert.equal(built.predicted_public_state.counter, 42n);
  const tx = ledger.Transaction.deserialize('signature', 'pre-proof', 'pre-binding', Buffer.from(built.transaction, 'hex'));
  assert.equal(tx.intents.size, 1);
  assert.ok(built.expires_at > Date.now());
  await assert.rejects(preparePublicCall({ ...request, circuit: 'needsWitness', arguments: [] }, dir), /private_witness_required/);
  const inclusion = { ...request, verified: true, operation: 'merkle', field: 'commitments', leaf_index: '0', leaf: '42' };
  const found = await explore(inclusion, dir);
  assert.equal(found.included, true); assert.equal(found.root, roots[0].root); assert.equal(found.path.path.length, 10);
  assert.deepEqual(found.snapshot, { block_hash: '0x' + 'ab'.repeat(32), block_height: 200 });
  const pinned = await explore({ ...inclusion, hash: found.snapshot.block_hash, root: found.root }, dir);
  assert.equal(pinned.included, true);
  assert.deepEqual(requests.at(-1).variables.offset, { blockOffset: { hash: 'ab'.repeat(32) } });
  assert.equal((await explore({ ...inclusion, leaf: '43' }, dir)).included, false);
  await assert.rejects(explore({ ...inclusion, leaf_index: '1024' }, dir), /invalid_merkle_leaf/);
  await assert.rejects(explore({ ...inclusion, leaf: '-1' }, dir), /invalid_merkle_leaf/);
  for (const [field, leaf, wrong] of [
    ['records', composite, { ...composite, active: true }], ['tuples', [true, '255'], [false, '255']],
    ['integers', '18446744073709551615', '18446744073709551614'], ['choices', 1, 0],
  ]) {
    assert.equal((await explore({ ...inclusion, field, leaf }, dir)).included, true, field);
    assert.equal((await explore({ ...inclusion, field, leaf: wrong }, dir)).included, false, field);
  }
  await assert.rejects(explore({ ...inclusion, field: 'integers', leaf: '18446744073709551616' }, dir), /invalid_merkle_leaf/);
  await assert.rejects(explore({ ...inclusion, field: 'records', leaf: { ...composite, owner: 'ab' } }, dir), /invalid_merkle_leaf/);
  assert.equal(roots.find(r => r.field === 'integers').leaf_type.maxval, '18446744073709551615');
  await assert.rejects(explore({ ...inclusion, field: 'items' }, dir), /public_tree_unavailable/);
  const changes = await explore({ ...request, verified: true, operation: 'changes', transaction_hash: '0x' + txHash }, dir);
  assert.equal(changes.scope, 'transaction'); assert.equal(changes.changes.find(c => c.field === 'counter').after, 1n);
  await assert.rejects(explore({ ...request, verified: true, operation: 'changes', transaction_hash: '0x' + 'ef'.repeat(32) }, dir), /transaction_snapshot_unavailable/);
  assert.ok(requests.some(q => q.variables?.offset?.transactionOffset?.hash === txHash));
  const infoPath = join(dir, 'compiler/contract-info.json');
  await writeFile(infoPath, JSON.stringify({ ...info, 'runtime-version': '0.16.0' }));
  const requestCount = requests.length;
  await assert.rejects(preparePublicCall(request, dir), /unsupported_runtime/);
  assert.equal(requests.length, requestCount, 'legacy runtime is rejected before network access');
});

test('public ledger uses fixed queries, exact transactions and paginated contract history', async t => {
  const id = 'ab'.repeat(32);
  const tx = { __typename: 'RegularTransaction', hash: id, zswapLedgerEvents: [] };
  const result = { transactions: [tx] };
  let request;
  t.mock.method(globalThis, 'fetch', async (_, init) => {
    request = JSON.parse(init.body);
    return Response.json({ data: result });
  });
  const params = { contract: '0x' + id, transaction_hash: '0x' + id };
  assert.deepEqual(await explore({ ...params, operation: 'transaction_zswap' }), { transactions: [tx] });
  assert.deepEqual(request.variables, { offset: { hash: id } });
  assert.match(request.query, /zswapLedgerEvents/);
  assert.deepEqual(await explore({ ...params, operation: 'transaction_events' }), { transactions: [tx] });
  assert.match(request.query, /contractEvents/);
  result.transactions = [];
  await assert.rejects(explore({ ...params, operation: 'transaction_events' }), /transaction_snapshot_unavailable/);
  result.contractEvents = [];
  assert.deepEqual(await explore({ contract: params.contract, operation: 'contract_events', page: 2 }), { contractEvents: [] });
  assert.deepEqual(request.variables, { filter: { contractAddress: id }, offset: 40, limit: 21 });
});

test('indexes finalized Mainnet and Preprod transaction fixtures without network access', async t => {
  const { readFile } = await import('node:fs/promises');
  const { indexContractEvents } = await import('./index-contract-events.mjs');
  const cases = JSON.parse(await readFile(new URL('./fixtures/indexed-contract-events.json', import.meta.url), 'utf8'));
  const fetch = t.mock.method(globalThis, 'fetch', () => { throw new Error('unexpected network read'); });
  for (const c of cases) {
    const request = { operation: 'index_contract_events', transaction_hash: c.hash, transaction_bytes: c.raw, outcome: 'success' };
    assert.deepEqual(await indexContractEvents(request), {hash: c.hash, source: 'local', events: c.logs});
    assert.deepEqual(await indexContractEvents({...request, outcome: 'failure'}), {hash: c.hash, source: 'local', events: []});
    if (c.logs.length) assert.deepEqual(await indexContractEvents({...request, outcome: 'partial_success'}), {hash: c.hash, source: 'pending'});
  }
  assert.equal(fetch.mock.callCount(), 0);
});

test('keeps unattributed logs on the transaction and paginates exact applied events', async t => {
  const { executedContractEvents } = await import('./public-ledger.mjs');
  const hash = '01'.repeat(32), block = '02'.repeat(32), address = '03'.repeat(32);
  let n = 0;
  let extraId = 2;
  t.mock.method(globalThis, 'fetch', async (_, init) => {
    const {query, variables} = JSON.parse(init.body);
    n++;
    if (query.includes('transactions(offset:')) return Response.json({data: {transactions: [{hash, __typename:'RegularTransaction', block: {hash: block},
      contractActions: [{address, contractEvents:[{id:1}, {id:2}]}, {address, contractEvents:[{id:extraId}]}]}]}});
    assert.deepEqual(variables.filter, {contractAddress:address, transactionHash:hash});
    const count = variables.offset === 0 ? 100 : 1;
    return Response.json({data: {contractEvents: Array.from({length:count}, (_, i) => ({
      id:variables.offset+i+1, __typename:'PausedEvent', contractAddress:address, raw:'aabb', transaction:{hash}
    }))}});
  });
  const events = await executedContractEvents('0x'+hash, '0x'+block);
  assert.equal(n,3);
  assert.equal(events.length,101);
  assert.equal(events[0].call_index,0);
  assert.equal(events[1].call_index,null);
  extraId = 102;
  await assert.rejects(executedContractEvents('0x'+hash, '0x'+block), /transaction_snapshot_unavailable/);
  assert.equal(events[100].call_index,null);
  await assert.rejects(executedContractEvents('0x'+hash, '0x'+address), /transaction_snapshot_unavailable/);
});

test('historical Ledger 8 call-free receipts are resolved offline and pinned to their hash', async t => {
  const legacy = await import('@midnight-ntwrk/midnight-js-protocol/v8');
  const { indexContractEvents } = await import('./index-contract-events.mjs');
  const intent = legacy.Intent.new(new Date(Date.now() + 300000)).addDeploy(new legacy.ContractDeploy(new legacy.ContractState()));
  const tx = legacy.Transaction.fromParts('preview', undefined, undefined, intent).mockProve();
  const hash = '0x' + tx.transactionHash();
  const request = { operation: 'index_contract_events', outcome: 'success', transaction_hash: hash,
    transaction_bytes: '0x' + Buffer.from(tx.serialize()).toString('hex') };
  const fetch = t.mock.method(globalThis, 'fetch', () => { throw new Error('unexpected external read'); });
  assert.deepEqual(await indexContractEvents(request), {hash, source: 'local', events: []});
  await assert.rejects(indexContractEvents({...request, transaction_hash: '0x' + 'ff'.repeat(32)}), /transaction_snapshot_unavailable/);
  assert.equal(fetch.mock.callCount(), 0);
});
