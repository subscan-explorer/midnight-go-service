import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import { localContractEvents } from './local-contract-events.mjs';
import { executedContractEvents } from './public-ledger.mjs';

export async function indexContractEvents(request) {
  const hash = request.transaction_hash;
  if (request.operation === 'executed_contract_events') return { hash, source: 'indexer', events: await executedContractEvents(hash, request.block_hash) };
  const local = request.transaction_bytes ? await localContractEvents(request.transaction_bytes.slice(2), hash.slice(2), request.outcome) : null;
  if (local !== null) return { hash, source: 'local', events: local };
  return { hash, source: 'pending' };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    console.log(JSON.stringify(await indexContractEvents(JSON.parse(readFileSync(0, 'utf8')))));
  } catch {
    console.error(JSON.stringify({ error_code: 'transaction_snapshot_unavailable' }));
    process.exitCode = 1;
  }
}
