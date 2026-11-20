# Public contract explorer operations

`POST /contract/explorer` expects an authenticated service boundary. Configure
authentication at the gateway. The endpoint prepares and reads public contract
data.

The JSON request contains `network_id`, `contract` (0x + 64 hex), `operation`:
`inspect`, `deployment`, `merkle`, `prepare`, `artifact`, `changes`, or `proof`. Optional fields
are `hash`, `transaction_hash`, `previous_transaction_hash`, `circuit`,
`arguments`, `coin_public_key`, `encryption_public_key`, `artifact`, `field`,
`leaf`, `leaf_hash`, `leaf_index`, `root`, `proof`. `verified` is overwritten from local artifact
availability. No caller-selected filesystem paths, provider URLs, private-state
objects or witness implementations are accepted. Unknown fields are rejected.

Example:

```json
{"network_id":"preview","contract":"0x<64 hex>","operation":"prepare","circuit":"set","arguments":["42"],"coin_public_key":"<public key>","encryption_public_key":"<public key>"}
```

`prepare` returns an unproven era-tagged serialized transaction and expected
public ledger state, expiry and the wallet public keys bound to the call. Supports compiled runtime 0.21 (Ledger 9) through Midnight.js 5. Old runtimes are outside this delivery and are rejected explicitly.
Compiler manifest integrity and the circuit's current on-chain verifier key are
checked. Witness calls throw, even when other circuits in the same contract can
execute publicly. The browser uses the official DApp Connector proof provider's
verifier-key registry, then wallet balancing/signing and submission.

`inspect` returns metadata, decoded public data when artifacts exist, serialized
state and all top-level Merkle roots. `hash` pins a block; `transaction_hash`
selects an exact action snapshot and checks the returned transaction identity.
`changes` compares exact action snapshots, using a predecessor chosen by Subscan;
absence is only valid for deployment. `merkle` derives the leaf type and tree depth
from verified compiler metadata; callers supply `field`, `leaf_index` and
`leaf_hash` or public JSON `leaf`. Field, Bytes, Boolean, Uint, Enum, Vector, Tuple and Struct (including
Maybe) are supported. Integer bounds remain lossless. The index must be below
2^depth (depth at most 32), leaf JSON at most 32 KiB, and metadata composition is
bounded to 20 levels/4,096 descriptor slots. Opaque values cannot be inserted
into Compact Merkle trees. The native path is checked with Midnight leaf/transient hashing.
It does not treat path existence as proof of inclusion. `artifact` returns
base64 compiler-verified prover/verifier/ZKIR bytes for one circuit. `proof`
returns proof-object presence/counts for an exact indexed regular transaction.

`deployment` requires `transaction_hash` and works without verified artifacts.
It decodes the original Ledger 9 transaction and matches exactly one
`ContractDeploy.address`. Its `initialState.operations()` supplies each circuit's
native `ContractOperation.serialize()` bytes, returned as `operations[]` with
`name`, `bytecode` (hex, no prefix), `byte_length`. Metadata includes `contract`,
`transaction_hash`, `era`, `protocol_version`, `segment_id`, zero-based
`action_index`, `source: contract_deploy`, and
`encoding: midnight-ledger-v9-contract-operation`. `transaction_bytes` is the
original raw transaction, separately labeled. Neither latest state nor the
post-deployment snapshot supplies bytecode. An empty operation list means the
matching deployment registered no circuits; a missing/ambiguous deployment is
`deployment_not_found`, not an empty successful result.

`inspect.merkle_trees[]` includes `field`, `root` (hex Field), `depth`, compiler
`leaf_type`, and `can_check_inclusion`. `merkle` returns these fields plus
`leaf_index`, `included`, and the native `path` only for a successful match.
Invalid leaf types/values/indices report `invalid_merkle_leaf`; unavailable trees
report `public_tree_unavailable`. Missing exact transaction data reports
`transaction_snapshot_unavailable`. These fixed codes use HTTP 422.

Limits: 64 KiB input; 32 KiB public argument JSON; existing state-process timeout,
concurrency admission, output and provider response caps. Requests cancel child
processes. There is no pagination. Errors use HTTP 400 for malformed input,
405 for method mismatch, 422 for unavailable public circuit/snapshot, 429/503
for existing admission handling, 504 for timeout and 5xx for provider/internal
failures. Unavailable proofs/state are never returned as a negative/empty result.

New public-network acceptance is opt-in. Local validation:

```sh
npm ci
npm run test:state
source ~/.gvm/scripts/gvm
gvm use go1.26.10
export GOTOOLCHAIN=local
go test -mod=mod -v ./...
go build ./...
go vet ./...
go build -o e2e/.midnight-server ./cmd
node --test e2e/http-cancellation.test.mjs e2e/preprod-rpc.test.mjs
```

The explorer integration test compiles Compact 0.36.0, runs actual circuits and
constructs a Ledger 9 transaction against a local mock Indexer. It checks witness
rejection, composite correct/incorrect Merkle leaves, range limits, exact deployment
selection among multiple actions, operation bytes versus state snapshots, and integer
precision without signing or broadcasting. Install Compact 0.36.0 to include it.

References: [Midnight.js 5 release](https://docs.midnight.network/relnotes/midnight-js/midnight-js-5-0-0)
and [browser DApp flow](https://docs.midnight.network/guides/browser-dapp) (its code
examples are Ledger 8; follow the versioned v5 interfaces for current networks).

### Contract Merkle verification

`inspect` returns `snapshot.block_hash` (and `block_height` when available).
Pass that block hash as `hash` on later inclusion requests. A transaction-bound
inspection returns `snapshot.transaction_hash` instead. Roots refer to that exact
snapshot; unavailable snapshots fail rather than falling back to current state.

For `merkle`, provide `field`, canonical decimal `leaf_index`, and exactly one
of `leaf_hash` (32-byte hex, optional `0x`) or `leaf` (public JSON only).
The browser hashes leaf values locally with Compact runtime 0.21.0 and sends
only `leaf_hash`; it also verifies successful returned paths locally. Existing
`insertHash` leaves use their original hash directly, without hashing it again.
This is membership verification, not an ownership, balance or unspent proof.

Optional `root` selects a hex Field root (with or without `0x`). It must equal
the selected tree's current root, or be accepted by that tree's
`HistoricMerkleTree.checkRoot` at the requested snapshot. A historical root
requires its original `proof`; current state cannot reconstruct an old path.
No unbounded history enumeration or leaf search is performed.

Optional `proof` is an object containing a `path` array with exactly `depth`
entries: `{ "sibling": { "field": "<decimal Field>" }, "goes_left": true }`.
Entries run from the leaf toward the root. `goes_left` means the current branch
is on the left. Directions must also match `leaf_index`. Extra embedded leaf
preimages are stripped in the browser before submission. Proof JSON is limited
to 16 KiB. Invalid encodings and out-of-range fields fail; a well-formed but
nonmatching path returns `included: false`.

Example (replace placeholders with real values):

```json
{"contract":"0x<64 hex>","operation":"merkle","hash":"0x<state block hash>","field":"commitments","leaf_index":"0","leaf_hash":"<64 hex>","root":"<root hex>"}
```

Responses include `snapshot`, `current_root`, checked `root`, `leaf_hash`,
`leaf_index`, `included`, and `path` (public siblings only, or null). Root
metadata includes `tree_type`, `supports_historical_roots` and an
`unavailable_reason` when inclusion checking is unsupported. Discovery covers
exported top-level Compact `MerkleTree` and `HistoricMerkleTree` fields with
supported verified types; this endpoint does not cover Zswap or DUST trees.

Fixed SDK error codes: `invalid_merkle_leaf`, `invalid_merkle_proof`,
`merkle_root_unavailable`, `historical_proof_required`, `public_tree_unavailable`.
The scan API translates these into fixed user-facing messages. No private
witnesses, signing keys or wallet access are needed. Hashes may still identify a
public commitment; a low-entropy preimage is not made confidential merely by
hashing it. Users must not send private leaf values to the legacy `leaf` API.

### Finalized event indexing

The Subscan worker calls `POST /contract/explorer` inside the existing service authentication
boundary. For `index_contract_events` it supplies the network, contract placeholder, exact
`transaction_hash`, canonical `block_hash`, pallet `outcome` and optional hex `transaction_bytes`
(up to 12 MiB of hex). This operation runs only the local Ledger 9 decoder and returns
`{hash, source: "local", events}` or `{hash, source: "pending"}`. Local Misc events include their
exact fixed-width name and payload. The optional integer `call_index` identifies a unique action.

For a pending result the worker calls `executed_contract_events` with the same identifiers and
outcome, omitting the bytes. The SDK checks the transaction block, fetches each distinct called
address in pages of 100 filtered by the exact transaction hash, and returns `source: "indexer"`
with events ordered by indexer ID. Missing snapshots, incomplete pages and upstream errors are
retryable. Both operations are internal to the SDK. Public Subscan callers use the existing
`transaction_events` and `contract_events` operations.
