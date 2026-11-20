import { ContractCall, ContractDeploy, CostModel, MaintenanceUpdate, Transaction, VmStack, runProgram } from '@midnight-ntwrk/midnight-js-protocol/v9';

const ledger8Prefix = Buffer.from('midnight:transaction[v9](');

export function decodeMiscEventFields(cell) {
  const nameSize = 32;
  const eventSize = nameSize + 256;
  if (cell.tag !== 'cell' || cell.content.value.length !== 1 || cell.content.alignment.length !== 1) return null;
  const [atom] = cell.content.alignment;
  const [value] = cell.content.value;
  if (atom.tag !== 'atom' || atom.value.tag !== 'bytes' || atom.value.length !== eventSize) return null;
  // The indexer restores trailing payload zeros only when the entire name is present on the wire.
  if (value.length < nameSize || value.length > eventSize) return null;
  const data = Buffer.alloc(eventSize);
  data.set(value);
  return { name: data.subarray(0, nameSize).toString('hex'), payload: data.subarray(nameSize).toString('hex') };
}

// A missing result requests authoritative execution events. An empty array proves that no
// call emitted a log. A transaction containing an unknown action is never classified empty.
export async function localContractEvents(raw, hash, outcome) {
  const bytes = Buffer.from(raw, 'hex');
  const legacy = bytes.subarray(0, ledger8Prefix.length).equals(ledger8Prefix);
  const classes = legacy ? await import('@midnight-ntwrk/midnight-js-protocol/v8') :
    { Transaction, ContractCall, ContractDeploy, MaintenanceUpdate };
  let tx;
  try {
    tx = classes.Transaction.deserialize('signature', 'proof', 'binding', bytes);
  } catch {
    return null;
  }
  if (tx.transactionHash() !== hash) throw new Error('transaction_snapshot_unavailable');
  if (outcome === 'failure') return [];
  if (!tx.intents || !['success', 'partial_success'].includes(outcome)) return null;
  const logs = [];
  let emittingCalls = 0;
  let actionIndex = -1;
  const calls = new Map();
  for (const intent of tx.intents.values()) {
    for (const action of intent.actions) {
      actionIndex++;
      if (action instanceof classes.ContractDeploy || action instanceof classes.MaintenanceUpdate) continue;
      if (!(action instanceof classes.ContractCall)) return null;
      const entry = Buffer.from(action.entryPoint).toString('hex');
      const key = action.address + ':' + entry;
      calls.set(key, calls.has(key) ? null : actionIndex);
      const guaranteed = action.guaranteedTranscript?.program ?? [];
      const fallible = action.fallibleTranscript?.program ?? [];
      if (fallible.includes('log')) return null;
      if (!guaranteed.includes('log')) continue;
      // Ledger 8 uses untyped logs. Only the proven-empty case shares the v9 projection.
      if (legacy) return null;
      // Preserve ledger ordering across physical and logical segments by delegating multi-call emission.
      if (outcome !== 'success' || ++emittingCalls > 1 ||
          guaranteed.some(op => typeof op === 'object' && ('branch' in op || 'jmp' in op))) return null;
      for (const [i, op] of guaranteed.entries()) {
        if (op !== 'log') continue;
        if (!guaranteed[i - 1]?.push) return null;
        let events;
        try {
          events = runProgram(new VmStack(), guaranteed.slice(i - 1, i + 1), CostModel.initialCostModel()).events;
        } catch {
          return null;
        }
        const item = events[0]?.content;
        if (events.length !== 1 || events[0].tag !== 'log' || item?.version !== 1 || item.eventType !== 'misc') return null;
        const fields = decodeMiscEventFields(item.data);
        if (!fields) return null;
        logs.push({ key, __typename: 'MiscContractEvent', contractAddress: action.address, ...fields });
      }
    }
  }
  return logs.map(({key, ...log}) => ({...log, call_index: calls.get(key)}));
}
