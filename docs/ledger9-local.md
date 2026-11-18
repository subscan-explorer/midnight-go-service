# Stagenet / Ledger 9 compatibility

This change preserves historical networks and adds opt-in Stagenet support. No chain
transaction, image publication or deployment is part of the change.

## Runtime and artifact selection

- State envelope `[v6]` uses the existing decoder; `[v8]` uses the additional 0.18
  runtime. Unknown envelopes fail explicitly. Indexer and Preprod RPC paths share
  this dispatch; unavailable historical state never falls back to current state.
- Generated contract readers select their declared runtime independently. Keep
  0.14, 0.15 and 0.16 and add 0.18.0-rc.1 / 0.20.0 aliases rather than replacing them.
- Preserve typed CMA `{tag,value}`; old string keys are marked `legacy`, not assigned
  a guessed algorithm. Legacy verification responses still return key strings.
- Stagenet permits Compact 0.35.0 / runtime 0.20.0 and optional boolean
  `compiler_options.feature_zkir_v3`. Reject unknown options and unsupported
  network/compiler combinations. Old requests default to disabled.
- Record compiler, options, source digest and network in the artifact manifest.

## Shared helper storage

Requests explicitly select `network_id=stagenet`; the shared helper needs
`MIDNIGHT_STAGENET_EPOCH`, matching the backend's network epoch.
Stagenet artifacts use `STATIC_DIR/verified/.networks/stagenet/<epoch>/verified/<address>`
inside the existing verified PVC. Legacy `STATIC_DIR/verified/<address>` is unchanged.
Temporary compiler files use `TEMP_DIR/stagenet/<epoch>`.

An epoch change selects new storage without deleting old data. Sharing this process
also shares memory, CPU and storage; verify old-network contracts and load before
upgrading the existing helper. Network identity/reset protection in the backend
remains separately required; an artifact directory does not detect chain resets.

## Validation

Follow AGENTS.md: Go 1.26.10 / GOTOOLCHAIN=local and `npm ci`.
Install Compact 0.29.0, 0.31.1 and 0.35.0; retain 0.31.1 as the operator default.

```sh
npm run test:state
RUN_COMPACT_035_TESTS=1 go test -mod=mod -v ./...
go build ./...
go vet ./...
go build -o e2e/.midnight-server ./cmd
node --test e2e/http-cancellation.test.mjs e2e/preprod-rpc.test.mjs
```

Tests cover old readers, recorded Stagenet state, typed CMA, strict options, shared-PVC
same-address isolation, epoch changes and real 0.35 compile/HTTP verification in both
ZKIR modes. Reference verifier keys come from independent local compiler runs;
these tests are not funded-wallet deployment or chain verifier-key acceptance.
The public Stagenet fixture records genesis, finalized hash, historical contract/hash
and observation time; resettable network samples must be revalidated before use.

Remaining acceptance: matching proof server/wallet/DUST for real deploy/call,
full scanner integration, complete public-event/C2C coverage and in-place Ledger 8→9
upgrade history. Selected old fixtures passing is not proof of every historical era.
