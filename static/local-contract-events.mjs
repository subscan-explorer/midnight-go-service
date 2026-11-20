import { ContractCall, ContractDeploy, CostModel, MaintenanceUpdate, Transaction, VmStack, runProgram } from '@midnight-ntwrk/midnight-js-protocol/v9';

// A missing result requests authoritative execution events. An empty array proves that no
// call emitted a log. A transaction containing an unknown action is never classified empty.
export function localContractEvents(raw, hash, outcome) {
  let tx;
  try {
    tx = Transaction.deserialize('signature', 'proof', 'binding', Buffer.from(raw, 'hex'));
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
      if (action instanceof ContractDeploy || action instanceof MaintenanceUpdate) continue;
      if (!(action instanceof ContractCall)) return null;
      const entry = Buffer.from(action.entryPoint).toString('hex');
      const key = action.address + ':' + entry;
      calls.set(key, calls.has(key) ? null : actionIndex);
      const guaranteed = action.guaranteedTranscript?.program ?? [];
      const fallible = action.fallibleTranscript?.program ?? [];
      if (fallible.includes('log')) return null;
      if (!guaranteed.includes('log')) continue;
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
        const cell = item.data;
        if (cell.tag !== 'cell' || cell.content.value.length !== 1 || cell.content.alignment.length !== 1) return null;
        const atom = cell.content.alignment[0];
        if (atom.tag !== 'atom' || atom.value.tag !== 'bytes' || atom.value.length !== 288 || cell.content.value[0].length > 288) return null;
        const data = Buffer.alloc(288);
        Buffer.from(cell.content.value[0]).copy(data);
        logs.push({ key, __typename: 'MiscContractEvent', contractAddress: action.address,
          name: data.subarray(0, 32).toString('hex'), payload: data.subarray(32).toString('hex') });
      }
    }
  }
  return logs.map(({key, ...log}) => ({...log, call_index: calls.get(key)}));
}
