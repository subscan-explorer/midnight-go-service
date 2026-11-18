# Preprod contract state through Node RPC

Preprod reads contract public state, maintenance authority and verifier keys
through native Node RPC, using Blockfrost by default. It does not contact the
Indexer. Preview and Mainnet retain their existing state providers.

## Configuration

Configure project IDs for the networks served by the helper. Default provider
URLs are built in; no additional endpoint variable is required.

| Network | Project ID environment variable | State provider |
| --- | --- | --- |
| Mainnet | `BLOCKFROST_PROJECT_ID` (existing setting) | Blockfrost Mainnet Indexer |
| Preprod | `MIDNIGHT_PREPROD_BLOCKFROST_PROJECT_ID` | Blockfrost Preprod Node RPC |
| Preview | None | Existing public Preview Indexer |

Preprod's RPC is `https://rpc.midnight-preprod.blockfrost.io`. The service appends
the URL-encoded Preprod project ID as `project_id`. Mainnet keeps its existing
configuration name; `MIDNIGHT_MAINNET_BLOCKFROST_PROJECT_ID` is an optional
network-specific override. The legacy `BLOCKFROST_PROJECT_ID` belongs only to
Mainnet, including when the default `NETWORK_ID` is Preview or Preprod. It never
fills a missing Preprod project ID. Missing credentials fail explicitly without
printing their values.

The [official network reference](https://docs.midnight.network/relnotes/network)
lists the Blockfrost endpoints and requires a separate Midnight Preprod project
token. Inject each network's Project ID from the deployment's secret store.
Keep credential values out of manifests, shell history, generated readers and logs.

Existing custom-node overrides remain supported: `MIDNIGHT_PREPROD_RPC_NODE`, or
`RPC_NODE` when `NETWORK_ID=preprod`. HTTP, HTTPS, WebSocket and secure WebSocket
are supported. Pass one authenticated URL rather than a scanner endpoint list.
A custom provider receives no Blockfrost project ID and does not require one.
Preprod Indexer settings are unused and do not need to be valid.

## Existing HTTP contract

`GET /contract/state` keeps its request and response format. `network_id=preprod`
and `networkid=preprod` select this provider. `block_hash` takes precedence over
the legacy `hash` alias. `view=cma` returns maintenance authority and operations
without loading a compiled ledger, including for a verified contract.

The provider strips the `0x` address prefix before calling
`midnight_contractState(address, blockHash)`. It preserves an explicit historical
hash and does not query or substitute the current head if history is missing.
Without a hash it calls `chain_getFinalizedHead` once, then pins the state read to
that hash. A WebSocket read reuses one connection for those two calls. Internal
block-height offsets resolve through `chain_getBlockHash` and use the same pinned
state path.

The native response is the same serialized state consumed by the existing
`ContractState.deserialize` decoder. Maintenance authority, operation names,
verifier bytes and the verified contract's compiled ledger reader are preserved.
The snapshot is the state at the end of the requested block; it does not expose
intermediate states between events in one block.

Empty snapshots produce the existing HTTP 500 state-read error. RPC errors,
missing blocks, invalid responses, decode failures, response limits and deadlines
also fail rather than returning an empty successful projection. Some node
versions return an empty string for an absent contract; this is recognized as
missing state. No fallback to the Indexer or latest state is attempted.

## Resource and diagnostic limits

Existing state admission, process concurrency, total timeout, cancellation and
output limits remain in force. `MIDNIGHT_RPC_RESPONSE_LIMIT_BYTES` caps HTTP bodies
and WebSocket messages before parsing, with a 32 MiB default. HTTP retries are
limited to connection failures, 429 and 5xx, within the same deadline. WebSocket
failures return immediately for the caller's normal retry policy.

Reader diagnostics use fixed `rpc_*`, `state_not_found`, `state_decode_failed` and
deadline classifications. Upstream URLs, credentials, JSON-RPC error messages,
response payloads and verifier keys are excluded from diagnostics.

## Tests and read-only acceptance

```bash
source ~/.gvm/scripts/gvm && gvm use go1.26.10
export GOTOOLCHAIN=local
npm run test:state
go test -mod=mod -v ./...
go build ./...
go vet ./...
go build -o e2e/.midnight-server ./cmd
node --test e2e/http-cancellation.test.mjs e2e/preprod-rpc.test.mjs
```

The local RPC tests cover HTTP and WebSocket, finalized and historical reads,
legacy HTTP aliases, Compact 0.29 ledger compatibility, CMA-only responses,
missing state, RPC/HTTP errors, malformed and oversized responses, cancellation,
credentials redaction, and isolation from unused Indexer configuration.
Configuration and process tests cover legacy Mainnet credentials, network-specific
project IDs, query encoding, missing credentials, and passing Preprod's default
authenticated Blockfrost RPC to the reader without writing credentials to disk.

To run read-only Blockfrost acceptance, inject a Midnight Preprod project ID as
`MIDNIGHT_PREPROD_BLOCKFROST_PROJECT_ID` through a trusted environment source,
leave custom RPC overrides unset, then run:

```bash
MIDNIGHT_PREPROD_LIVE=1 node --test --test-name-pattern='configured RPC acceptance' e2e/preprod-rpc.test.mjs
```

The live gate reads fixed hashes around maintenance event `2923941-3`, including
the following block with unchanged state, plus deployment snapshots at blocks
32,920 and 3,064,968 and absence before the latter deployment. It checks
maintenance counters, verifier keys and the early contract's
committee/key digests against the researched indexed records. It also checks a
latest finalized read. Missing credentials in an explicitly enabled live gate
fail the test rather than skipping it. The default CI run skips live acceptance.
An existing custom node can run the same gate with `MIDNIGHT_PREPROD_RPC_NODE`.
Its successful snapshots do not establish Blockfrost archive coverage; rerun the
gate against Blockfrost after provisioning the Preprod project ID.

## Decision and boundaries

- Use the native `midnight_contractState` RPC rather than manually encoding
  runtime-API arguments and unwrapping SCALE results; retain the existing SDK and
  compiled ledger decoders.
- Limit the provider change to Preprod, whose current and historical RPC behavior
  was verified against the existing node. Use built-in Blockfrost endpoints and
  separate project IDs on shared helpers, retaining the legacy Mainnet key.
- Historical reads require retained runtime and ledger state. Successful sampled
  heights do not establish full archive completeness or production throughput.
- Local tests and read-only acceptance do not deploy the helper or establish that
  the production retry backlog has drained. Those remain later acceptance gates.
