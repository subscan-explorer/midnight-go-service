import * as runtime from '@midnight-ntwrk/compact-runtime';
import * as contract from './reader-031.js';

// Serialized real runtime state exercises the provider and the cross-runtime
// ledger reader without a wallet, live Indexer or compiler installation.
export function stateFixture(counter = 2n) {
  const state = new contract.Contract({}).initialState(runtime.createConstructorContext({}, '00'.repeat(32))).currentContractState;
  state.maintenanceAuthority = new runtime.ContractMaintenanceAuthority([], 0, counter);
  state.setOperation('fixture', new runtime.ContractOperation());
  return Buffer.from(state.serialize()).toString('hex');
}
