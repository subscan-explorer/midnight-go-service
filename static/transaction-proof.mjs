import { indexedTransaction } from './public-ledger.mjs';

// Count proof objects, never transaction labels, amounts, witnesses or preimages.
export function proofPresence(transaction) {
  const counts = { contract: 0, shielded: 0, dust: 0 };
  const present = proof => proof?.instance === 'proof';
  for (const intent of transaction.intents?.values() ?? []) {
    for (const action of intent.actions) if (present(action.proof)) counts.contract++;
    for (const spend of intent.dustActions?.spends ?? []) if (present(spend.proof)) counts.dust++;
  }
  for (const offer of [transaction.guaranteedOffer, ...transaction.fallibleOffer?.values() ?? []]) {
    if (!offer) continue;
    for (const item of [...offer.inputs, ...offer.outputs]) if (present(item.proof)) counts.shielded++;
    for (const item of offer.transients) {
      if (present(item.inputProof)) counts.shielded++;
      if (present(item.outputProof)) counts.shielded++;
    }
  }
  return { has_zk_proof: Object.values(counts).some(count => count > 0), proof_count: counts };
}

async function readTransaction(hash) {
  const record = await indexedTransaction(hash, 'hash raw protocolVersion');
  if (typeof record.raw !== 'string' || !/^(?:[a-f0-9]{2})+$/i.test(record.raw)) throw new Error('transaction_snapshot_unavailable');
  const { protocolVersionToLedger } = await import('@midnight-ntwrk/midnight-js-protocol/version');
  const era = protocolVersionToLedger(record.protocolVersion);
  if (era !== 'v9') throw new Error('unsupported_runtime');
  const ledger = await import('@midnight-ntwrk/midnight-js-protocol/v9');
  const transaction = ledger.Transaction.deserialize('signature', 'proof', 'binding', Buffer.from(record.raw, 'hex'));
  return { transaction, ledger, record, era };
}

export async function transactionProof(hash) {
  const { transaction, era } = await readTransaction(hash);
  return { ...proofPresence(transaction), transaction_hash: hash, era };
}

// Deployment code is the native operation serialization inside ContractDeploy,
// not the contract's later state snapshot. Preserve exact transaction provenance.
export async function deploymentBytecode(hash, address) {
  const { transaction, ledger, record, era } = await readTransaction(hash);
  const matches = [];
  for (const [segment, intent] of transaction.intents ?? []) {
    for (const [index, action] of intent.actions.entries()) {
      if (action instanceof ledger.ContractDeploy && action.address.toLowerCase() === address.replace(/^0x/, '').toLowerCase()) {
        matches.push({ segment, index, state: action.initialState });
      }
    }
  }
  if (matches.length !== 1) throw new Error('deployment_not_found');
  const { state, segment, index } = matches[0];
  return {
    contract: address, transaction_hash: hash, era, protocol_version: record.protocolVersion,
    segment_id: segment, action_index: index, source: 'contract_deploy', encoding: 'midnight-ledger-v9-contract-operation',
    operations: state.operations().map(name => {
      const operation = state.operation(name);
      if (!operation) throw new Error('deployment_not_found');
      const bytes = operation.serialize();
      return { name: typeof name === 'string' ? name : '0x' + Buffer.from(name).toString('hex'),
        bytecode: Buffer.from(bytes).toString('hex'), byte_length: bytes.length };
    }),
    transaction_bytes: record.raw,
  };
}
