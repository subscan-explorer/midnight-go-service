import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import * as runtime from '@midnight-ntwrk/compact-runtime-0.21';
import { checkMerkleInclusion, merkleRoots } from './merkle.mjs';
import { readCompilerInfo } from './public-arguments.mjs';
import { stringifyState } from './state.mjs';

test('compiled Compact trees validate hashes, imported paths, indices and accepted historic roots without preimages', async t => {
  const directory = await mkdtemp(join(tmpdir(), 'contract-merkle-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  await promisify(execFile)('compact', ['compile', '+0.36.0', '--skip-zk', new URL('./fixtures/merkle.compact', import.meta.url).pathname, directory]);
  const module = await import(pathToFileURL(join(directory, 'contract/index.js')).href);
  const info = await readCompilerInfo(directory);
  const contract = new module.Contract({});
  const state = (await contract.initialState(runtime.createConstructorContext(undefined, '00'.repeat(32)))).currentContractState;
  const call = async (name, ...args) => {
    const ctx = runtime.createCircuitContext({ circuitId: name, contractAddress: 'ab'.repeat(32), coinPublicKeyOrZswapState: '00'.repeat(32), contractState: state.data, privateState: undefined });
    const result = await contract.circuits[name](ctx, ...args);
    state.data = new runtime.ChargedState(result.context.callContext.currentQueryContext.state.state);
  };
  const hash = value => Buffer.from(new runtime.CompactTypeBytes(32).fromValue(runtime.leafHash({ value: runtime.CompactTypeField.toValue(value), alignment: runtime.CompactTypeField.alignment() }).value)).toString('hex');
  const publicHash = '1234567890abcdef'.repeat(4);
  await call('insert', 7n, Buffer.from(publicHash, 'hex'));
  const request = { field: 'values', leaf_index: '0', leaf_hash: hash(7n) };
  const before = module.ledger(state.data.state);
  const historic = checkMerkleInclusion({ ...request, field: 'history' }, before, info, runtime);
  assert.equal(historic.included, true);
  assert.equal(historic.supports_historical_roots, true);
  assert.equal(Object.hasOwn(historic.path, 'leaf'), false);
  await call('insert', 42n, Buffer.from('ab'.repeat(32), 'hex'));
  await call('insert', 100n, Buffer.from('cd'.repeat(32), 'hex'));
  const data = module.ledger(state.data.state);
  const roots = merkleRoots(data, info, runtime);
  assert.equal(roots.length, 3);
  assert.ok(roots.every(r => r.can_check_inclusion));
  const vector = checkMerkleInclusion(request, data, info, runtime);
  assert.equal(vector.included, true);
  for (const [index, value] of [7n, 42n, 100n].entries()) {
    assert.equal(checkMerkleInclusion({ ...request, leaf_index: String(index), leaf_hash: hash(value) }, data, info, runtime).included, true);
  }
  assert.equal(checkMerkleInclusion({ ...request, field: 'hashes', leaf_hash: publicHash }, data, info, runtime).included, true);
  assert.equal(checkMerkleInclusion({ ...request, leaf_index: '1' }, data, info, runtime).included, false);
  assert.equal(checkMerkleInclusion({ ...request, leaf_index: '3' }, data, info, runtime).included, false);
  assert.equal(checkMerkleInclusion({ ...request, leaf_hash: hash(8n) }, data, info, runtime).included, false);
  assert.equal(checkMerkleInclusion({ ...request, proof: vector.path, root: vector.root }, data, info, runtime).included, true);
  assert.equal(checkMerkleInclusion({ ...request, proof: vector.path, leaf_index: '1' }, data, info, runtime).included, false);
  // Exercise the production subprocess serializer, including zero siblings and
  // large Field siblings. Its generic BigInt conversion must not alter proofs.
  const json = value => JSON.parse(stringifyState(value));
  const wire = json(vector);
  assert.ok(wire.path.path.some(entry => entry.sibling.field === '0'));
  assert.ok(wire.path.path.some(entry => BigInt(entry.sibling.field) > BigInt(Number.MAX_SAFE_INTEGER)));
  assert.ok(wire.path.path.every(entry => typeof entry.sibling.field === 'string' && /^(0|[1-9][0-9]*)$/.test(entry.sibling.field)));
  assert.equal(checkMerkleInclusion({ ...request, proof: wire.path }, data, info, runtime).included, true);
  const extraFields = json(wire.path);
  extraFields.path[0].leaf = 'do not echo a supplied preimage';
  assert.deepEqual(json(checkMerkleInclusion({ ...request, proof: extraFields }, data, info, runtime)).path, wire.path);
  const tampered = json(vector.path);
  tampered.path[1].sibling.field = '1';
  assert.equal(checkMerkleInclusion({ ...request, proof: tampered }, data, info, runtime).included, false);
  assert.throws(() => checkMerkleInclusion({ ...request, root: '1' }, data, info, runtime), /merkle_root_unavailable/);
  assert.throws(() => checkMerkleInclusion({ ...request, proof: { path: [] } }, data, info, runtime), /invalid_merkle_proof/);
  assert.throws(() => checkMerkleInclusion({ ...request, leaf: '7' }, data, info, runtime), /invalid_merkle_leaf/);
  assert.throws(() => checkMerkleInclusion({ ...request, leaf_index: '16' }, data, info, runtime), /invalid_merkle_leaf/);
  const old = { ...request, field: 'history', root: historic.root, proof: historic.path };
  assert.equal(checkMerkleInclusion(old, data, info, runtime).included, true);
  assert.throws(() => checkMerkleInclusion({ ...old, proof: undefined }, data, info, runtime), /historical_proof_required/);
  await call('clearHistory');
  assert.throws(() => checkMerkleInclusion(old, module.ledger(state.data.state), info, runtime), /merkle_root_unavailable/);
  if (process.env.MERKLE_VECTOR_OUTPUT) await writeFile(process.env.MERKLE_VECTOR_OUTPUT, JSON.stringify(json({ source: 'Compact 0.36.0 / runtime 0.21.0; merkle.compact values [7, 42, 100]', value: '7', ...vector }), null, 2) + '\n');
});
