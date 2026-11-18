import { readContractState, stateResult, replacer } from './state.mjs';

try {
    const state = await readContractState(process.argv[2], process.argv[3]);
    console.log(JSON.stringify(stateResult(state), replacer));
} catch {
    // Provider errors may contain credential-bearing URLs. Keep them off stdout/stderr.
    console.error('Contract maintenance state read failed');
    process.exitCode = 1;
}
