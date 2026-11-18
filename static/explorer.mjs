import { pathToFileURL } from 'node:url';
import { join } from 'node:path';
import { boundedJSON, compiledLedger, decodeState, readContractSnapshot, stateResult, stringifyState } from './state.mjs';
import { circuitArguments, readCompilerInfo } from './public-arguments.mjs';
import { contractRuntime } from './runtime-loader.mjs';
import { checkMerkleInclusion, merkleRoots } from './merkle.mjs';

// This subprocess receives public arguments only. It has no wallet secret,
// private-state provider or usable witness implementations.
export async function preparePublicCall(request, directory) {
  const expiresAt = Date.now() + 240000;
  const [{ createUnprovenCallTx, verifyContractState }, { NodeZkConfigProvider }, { indexerPublicDataProvider }, { CompiledContract }] = await Promise.all([
    import('@midnight-ntwrk/midnight-js-contracts-v5'),
    import('@midnight-ntwrk/midnight-js-node-zk-config-provider-v5'),
    import('@midnight-ntwrk/midnight-js-indexer-public-data-provider-v5'),
    import('@midnight-ntwrk/midnight-js-protocol/v9/compact-js'),
  ]);
  const info = await readCompilerInfo(directory);
  const args = circuitArguments(info, request.circuit, request.arguments);
  // This delivery targets the current Ledger 9 runtime only.
  if (info['runtime-version'] !== '0.21.0') throw new Error('unsupported_runtime');
  const module = await import(pathToFileURL(join(directory, 'contract/index.js')).href);
  let witnessRequired = false;
  const witnesses = Object.fromEntries((info.witnesses ?? []).map(w => [w.name, () => { witnessRequired = true; throw new Error('private_witness_required'); }]));
  const compiledContract = CompiledContract.make('explorer', module.Contract).pipe(CompiledContract.withWitnesses(witnesses), CompiledContract.withCompiledFileAssets(directory));
  const zkConfigProvider = new NodeZkConfigProvider(directory);
  const publicDataProvider = indexerPublicDataProvider({ queryURL: process.env.INDEXER_RPC, subscriptionURL: process.env.INDEXER_WS });
  try {
    const address = request.contract.replace(/^0x/, '');
    const snapshot = await publicDataProvider.queryContractState(address);
    if (!snapshot) throw new Error('contract_state_unavailable');
    await verifyContractState([[request.circuit, await zkConfigProvider.getVerifierKey(request.circuit)]], snapshot.contractState, address);
    const built = await createUnprovenCallTx({
      zkConfigProvider, publicDataProvider,
      config: { networkId: process.env.MIDNIGHT_NETWORK_ID, ttlSeconds: 300 },
      walletProvider: {
        getCoinPublicKey: () => request.coin_public_key,
        getEncryptionPublicKey: () => request.encryption_public_key,
      },
    }, { compiledContract, contractAddress: address, circuitId: request.circuit, args });
    const handle = built.private.unprovenTx;
    const readLedger = await compiledLedger(pathToFileURL(join(directory, 'contract/index.js')), module.ledger);
    // Never spread built.private: it can contain transcripts and private coins.
    return { era: handle.era, transaction: Buffer.from(handle.serialize()).toString('hex'), network_id: process.env.MIDNIGHT_NETWORK_ID,
      contract: request.contract, circuit: request.circuit, expires_at: expiresAt,
      coin_public_key: request.coin_public_key, encryption_public_key: request.encryption_public_key,
      predicted_public_state: readLedger(built.public.nextContractState) };
  } catch (error) {
    if (witnessRequired) throw new Error('private_witness_required');
    throw error;
  } finally { publicDataProvider.dispose(); }
}

async function actionState(address, hash) {
  const response = await fetch(process.env.INDEXER_RPC, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    signal: AbortSignal.timeout(Number(process.env.MIDNIGHT_STATE_TIMEOUT_MS) || 45000),
    body: JSON.stringify({ query: `query ExplorerAction($address: HexEncoded!, $offset: ContractActionOffset!) {
      contractAction(address: $address, offset: $offset) { state transaction { hash } }
    }`, variables: { address: address.replace(/^0x/, ''), offset: { transactionOffset: { hash: hash.replace(/^0x/, '') } } } }),
  });
  if (!response.ok) throw new Error('indexer_unavailable');
  const result = await boundedJSON(response, Number(process.env.MIDNIGHT_INDEXER_RESPONSE_LIMIT_BYTES) || 32 << 20);
  const action = result.data?.contractAction;
  if (result.errors?.length || !action || action.transaction.hash.toLowerCase() !== hash.replace(/^0x/, '').toLowerCase()) throw new Error('transaction_snapshot_unavailable');
  return decodeState(action.state);
}

export function publicStateChanges(before, after) {
  return [...new Set([...Object.keys(before ?? {}), ...Object.keys(after ?? {})])].sort().flatMap(field => {
    const previous = before?.[field], current = after?.[field];
    if (stringifyState({ value: previous }) === stringifyState({ value: current })) return [];
    return [{ field, before: previous ?? null, after: current ?? null,
      kind: previous === undefined ? 'added' : current === undefined ? 'removed' : 'changed' }];
  });
}

export async function explore(request, directory) {
  if (request.operation === 'deployment') {
    const { deploymentBytecode } = await import('./transaction-proof.mjs');
    return deploymentBytecode(request.transaction_hash, request.contract);
  }
  if (request.operation === 'proof') {
    const { transactionProof } = await import('./transaction-proof.mjs');
    return transactionProof(request.transaction_hash);
  }
  if (request.operation === 'prepare') return preparePublicCall(request, directory);
  if (request.operation === 'artifact') {
    const { NodeZkConfigProvider } = await import('@midnight-ntwrk/midnight-js-node-zk-config-provider-v5');
    const provider = new NodeZkConfigProvider(directory);
    const methods = { prover: 'getProverKey', verifier: 'getVerifierKey', zkir: 'getZKIR' };
    const bytes = await provider[methods[request.artifact]](request.circuit);
    return { data: Buffer.from(bytes).toString('base64') };
  }
  const moduleURL = pathToFileURL(join(directory, 'contract/index.js'));
  let ledger, info, runtime;
  if (request.verified) {
    info = await readCompilerInfo(directory);
    if (info['runtime-version'] !== '0.21.0') throw new Error('unsupported_runtime');
    const module = await import(moduleURL.href);
    ledger = await compiledLedger(moduleURL, module.ledger);
    runtime = await contractRuntime(moduleURL);
  }
  if (request.operation === 'changes') {
    const after = stateResult(await actionState(request.contract, request.transaction_hash), ledger);
    const before = request.previous_transaction_hash
      ? stateResult(await actionState(request.contract, request.previous_transaction_hash), ledger) : null;
    return { scope: 'transaction', transaction_hash: request.transaction_hash, previous_transaction_hash: request.previous_transaction_hash || null,
      changes: publicStateChanges(before?.data, after.data), metadata_changes: publicStateChanges(before && { operations: before.operations, maintenanceAuthority: before.maintenanceAuthority },
        { operations: after.operations, maintenanceAuthority: after.maintenanceAuthority }) };
  }
  const { state, snapshot } = request.transaction_hash
    ? { state: await actionState(request.contract, request.transaction_hash), snapshot: { transaction_hash: request.transaction_hash } }
    : await readContractSnapshot(request.contract.replace(/^0x/, ''), request.hash);
  const result = stateResult(state, ledger);
  if (request.operation === 'inspect') return { ...result, snapshot, serialized_state: Buffer.from(state.serialize()).toString('hex'), merkle_trees: merkleRoots(result.data, info, runtime) };
  return { ...checkMerkleInclusion(request, result.data, info, runtime), snapshot };
}

if (process.argv[1] === new URL(import.meta.url).pathname) {
  try {
    console.log(stringifyState(await explore(JSON.parse(process.argv[2]), process.argv[3])));
  } catch (error) {
    // Fixed codes only: provider errors can contain credentials and payloads.
    const allowed = ['private_witness_required', 'unsupported_runtime', 'public_tree_unavailable', 'invalid_merkle_leaf', 'invalid_merkle_proof', 'merkle_root_unavailable', 'historical_proof_required', 'transaction_snapshot_unavailable', 'deployment_not_found'];
    console.error(JSON.stringify({ error_code: allowed.find(code => String(error).includes(code)) ?? 'explorer_failed' }));
    process.exitCode = 1;
  }
}
