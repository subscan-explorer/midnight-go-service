import { boundedJSON } from './state.mjs';

const logFields = 'id __typename contractAddress raw transaction { hash } ... on MiscContractEvent { name payload }';
const pageSize = 20;

async function query(operation, variables) {
  const response = await fetch(process.env.INDEXER_RPC, {
    method: 'POST', redirect: 'error', headers: { 'Content-Type': 'application/json' },
    signal: AbortSignal.timeout(Number(process.env.MIDNIGHT_STATE_TIMEOUT_MS) || 45000),
    body: JSON.stringify({ query: operation, variables }),
  });
  if (!response.ok) throw new Error('transaction_snapshot_unavailable');
  const result = await boundedJSON(response, Number(process.env.MIDNIGHT_INDEXER_RESPONSE_LIMIT_BYTES) || 32 << 20);
  if (result.errors?.length || !result.data) throw new Error('transaction_snapshot_unavailable');
  return result.data;
}

export async function indexedTransaction(hash, fields) {
  const id = hash.replace(/^0x/, '').toLowerCase();
  const { transactions } = await query(`query($offset: TransactionOffset!) { transactions(offset: $offset) { ${fields} } }`, { offset: { hash: id } });
  if (transactions?.length !== 1 || transactions[0]?.hash?.toLowerCase() !== id) throw new Error('transaction_snapshot_unavailable');
  return transactions[0];
}

export async function publicLedger(request) {
  if (request.operation === 'contract_events') {
    const { contractEvents } = await query(`query($filter: ContractEventFilter!, $offset: Int!, $limit: Int!) {
      contractEvents(filter: $filter, offset: $offset, limit: $limit) { ${logFields} }
    }`, { filter: { contractAddress: request.contract.slice(2) }, offset: (request.page ?? 0) * pageSize, limit: pageSize + 1 });
    return { contractEvents };
  }
  const fields = request.operation === 'transaction_zswap'
    ? 'zswapLedgerEvents { id raw }'
    : `contractActions { ... on ContractCall { contractEvents { ${logFields} } } }`;
  const tx = await indexedTransaction(request.transaction_hash, `hash __typename ... on RegularTransaction { ${fields} }`);
  return { transactions: [tx] };
}

// Only the top-level filter includes logs whose call attribution is ambiguous. Pin every
// page to the transaction and take its block hash from the same finalized indexer.
export async function executedContractEvents(hash, blockHash) {
  const id = hash.slice(2);
  const tx = await indexedTransaction(hash, 'hash block { hash } __typename ... on RegularTransaction { contractActions { ... on ContractCall { address contractEvents { id } } } }');
  if (tx.block?.hash !== blockHash.slice(2) || tx.__typename !== 'RegularTransaction') throw new Error('transaction_snapshot_unavailable');
  const addresses = new Set(tx.contractActions.flatMap(a => a.address ? [a.address] : []));
  const attributed = new Map();
  for (const [index, action] of tx.contractActions.entries()) {
    for (const event of action.contractEvents ?? []) attributed.set(event.id, index);
  }
  const events = [];
  for (const address of addresses) {
    for (let offset = 0; ; offset += 100) {
      const { contractEvents: page } = await query(`query($filter: ContractEventFilter!, $offset: Int!) {
        contractEvents(filter: $filter, offset: $offset, limit: 100) { ${logFields} }
      }`, { filter: { contractAddress: address, transactionHash: id }, offset });
      if (!Array.isArray(page) || page.length > 100) throw new Error('transaction_snapshot_unavailable');
      for (const event of page) {
        if (event.contractAddress !== address || event.transaction?.hash !== id || !Number.isSafeInteger(event.id) ||
            !event.raw || events.length >= 16384) throw new Error('transaction_snapshot_unavailable');
        events.push({...event, call_index: attributed.get(event.id) ?? null});
      }
      if (page.length < 100) break;
    }
  }
  events.sort((a, b) => a.id - b.id);
  if (events.some((e, i) => i && events[i - 1].id === e.id)) throw new Error('transaction_snapshot_unavailable');
  return events;
}
