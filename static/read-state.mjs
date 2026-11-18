import { pathToFileURL } from 'node:url';
import { readContractState, stateResult, compiledLedger, stringifyState, readerDiagnostic } from './state.mjs';

try {
  const [address, hash, contractPath] = process.argv.slice(2);
  // Importing the compiled reader and fetching state are independent. Keep each
  // request in its own process because the SDK's network ID is process-global.
  const ledgerPromise = contractPath ? (async () => {
    const moduleURL = pathToFileURL(contractPath);
    const contract = await import(moduleURL.href);
    return compiledLedger(moduleURL, contract.ledger);
  })() : undefined;
  const [state, ledger] = await Promise.all([readContractState(address, hash), ledgerPromise]);
  console.log(stringifyState(stateResult(state, ledger)));
} catch (error) {
  // Provider failures can contain credentials. Never include them in output.
  console.error(JSON.stringify(readerDiagnostic(error)));
  process.exitCode = 1;
}
