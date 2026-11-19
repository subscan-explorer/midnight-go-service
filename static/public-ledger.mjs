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
