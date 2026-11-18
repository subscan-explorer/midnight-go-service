# Midnight Go SDK

A lightweight service to verify Midnight Compact contracts and read contract public state. Supports multiple Compact compiler versions.

- Contract verification: compares on-chain verification keys to ensure source matches the deployed contract
- Public state reading: reads contract public state at the latest block or a specified block hash
- Multi-compiler support: several Compact versions available (see below)

The default network is Preview. Mainnet and Preprod contract states come directly from Node RPC, using Blockfrost by default with network-specific project IDs; other networks retain their Indexer GraphQL readers. Readers retain Midnight JS 4.1.1 and Compact runtime 0.16.0 for historical Ledger 8 artifacts, with pinned aliases for other declared runtimes, including 0.21.0 for Ledger 9. Unknown runtime declarations fail explicitly.

## Requirements
- Go 1.26.10 (`go.mod`, Docker and CI use the same patched compiler)
- Node.js v22+
- Midnight Compact compiler installed (override with COMPACT_BIN, default `compact`)

## Install
```bash
source ~/.gvm/scripts/gvm
gvm install go1.26.10 -B # First-time setup only
gvm use go1.26.10
export GOTOOLCHAIN=local
npm ci
go mod download
go build -o midnight-server ./cmd
```

On this development machine, select Go through GVM before every Go session; the
`/usr/local/go/bin/go` installation is not usable. `GOTOOLCHAIN=local` prevents an
implicit compiler download from masking a version mismatch. Docker builds a
static Go binary so it can run with the Node image's system libraries.

## Run
```bash
./midnight-server
```

Optional: pre-download supported Compact compiler versions
```bash
./midnight-server download
```

Environment variables
- STATIC_DIR: directory to store verified contracts and generated scripts, default `static`
- TEMP_DIR: temporary directory for compilation, default `tmp`
- COMPACT_BIN: Compact compiler executable name/path, default `compact`
- NETWORK_ID: default request network, default `preview`
- INDEXER_RPC / INDEXER_WS / RPC_NODE: endpoint overrides for `NETWORK_ID`
- MIDNIGHT_MAINNET_INDEXER_RPC / MIDNIGHT_MAINNET_INDEXER_WS / MIDNIGHT_MAINNET_RPC_NODE: Mainnet overrides preserved when each HTTP request selects Mainnet. The same prefix convention applies to Preview and Preprod.
- BLOCKFROST_PROJECT_ID: existing Midnight Mainnet project token setting, required for default Mainnet endpoints. `MIDNIGHT_MAINNET_BLOCKFROST_PROJECT_ID` can override it. This legacy key is never used for Preprod.
- MIDNIGHT_PREPROD_BLOCKFROST_PROJECT_ID: Midnight Preprod project token, required for its default Blockfrost RPC. Endpoint URLs are built in; no additional endpoint configuration is needed. Preprod state reads use RPC rather than the Indexer. See [Preprod RPC configuration and acceptance](docs/preprod-rpc.md).
- Inject project IDs from secrets. The service URL-encodes `project_id` only for Blockfrost hosts of the selected network. Existing custom endpoint overrides remain supported and receive no Blockfrost token.
- MIDNIGHT_COMPILE_CONCURRENCY: simultaneous `compact` compiles, default `1`
- MIDNIGHT_COMPILE_TIMEOUT: one compile, default `5m`
- MIDNIGHT_STATE_CONCURRENCY: simultaneous contract-state Node processes, default `1`
- MIDNIGHT_STATE_TIMEOUT: state-read budget including queue time, default `45s`
- MIDNIGHT_VERIFY_MAX_REQUESTS: admitted verification requests through response completion, default compile concurrency + 1 (`2`)
- MIDNIGHT_STATE_MAX_REQUESTS: admitted state requests through response completion, default state concurrency + 7 (`8`)
- MIDNIGHT_VERIFY_BODY_LIMIT_BYTES: verification JSON body cap, default 8 MiB; oversized bodies return HTTP 413
- MIDNIGHT_INDEXER_RESPONSE_LIMIT_BYTES: decoded HTTP response body cap before parsing Indexer JSON, default 32 MiB
- MIDNIGHT_RPC_RESPONSE_LIMIT_BYTES: RPC HTTP body / WebSocket message cap before JSON parsing, default 32 MiB
- MIDNIGHT_STATE_OUTPUT_LIMIT_BYTES: complete reader JSON cap, default 16 MiB; oversize output fails without returning truncated state
- MIDNIGHT_OUTPUT_LIMIT_BYTES: retained compiler stdout/stderr and state-reader stderr, default 1 MiB. State-reader stdout is the JSON response and is not truncated.
- MIDNIGHT_HTTP_READ_HEADER_TIMEOUT / MIDNIGHT_HTTP_READ_TIMEOUT / MIDNIGHT_HTTP_WRITE_TIMEOUT / MIDNIGHT_HTTP_IDLE_TIMEOUT: HTTP server deadlines, defaults `10s`, `5m`, `10m30s`, `90s`. An explicit handler context deadline also bounds queue waits and processing, reserving the smaller of 1 second or 10% of the write timeout for the response.

Mainnet defaults are `https://midnight-mainnet.blockfrost.io/api/v0`, `wss://midnight-mainnet.blockfrost.io/api/v0/ws` and `https://rpc.midnight-mainnet.blockfrost.io`. The old Midnight-hosted Mainnet endpoints were retired; see the official [migration guide](https://docs.midnight.network/guides/networks-and-environments#migrating-from-the-midnight-hosted-mainnet-endpoints). Configure all three overrides to use an independent provider without Blockfrost credentials.

Preprod defaults to `https://rpc.midnight-preprod.blockfrost.io` for state reads. Its project ID must belong to Midnight Preprod; a Mainnet project ID cannot authenticate Preprod. Preview keeps its existing public endpoints and requires no Blockfrost project ID. See the official [network reference](https://docs.midnight.network/relnotes/network).

Readers receive endpoints through their process environment, including per-request network overrides. Credentials are never written into generated scripts. Request cancellation terminates the reader process and errors never return an empty successful state.

Contract reads use one fixed JavaScript entry point. Verified contract reads load the existing compiled `contract/index.js` directly, avoiding per-request script generation and TypeScript transpilation. Compiled-reader loading overlaps the state query. Each request keeps its own Node process to isolate the SDK's global network ID; existing `.ts` readers remain supported by `ExecTsScript` for compatibility. Mainnet and Preprod use `midnight_contractState` at the requested block hash, or resolve the finalized head once when no hash is provided. Each read reuses one WebSocket connection when configured with `ws`/`wss`. Other networks use bounded HTTP GraphQL queries. HTTP transport retries connection failures, HTTP 429 and 5xx up to five attempts within the shared deadline; RPC application errors, malformed state and response limits fail immediately. The official runtime still decodes state, and state responses are not cached.

Extra compiles and state reads wait for a slot; an expired request leaves the queue without running. Verification requests for the same contract serialize the existence check, compilation, verification, and cleanup so a canceled waiter cannot delete another request’s files. Client disconnect cancels the wait and kills the compiler or reader process group. Admission is checked before body decoding; excess requests return HTTP 503 with Retry-After. Limits cover queued requests and response serialization, in addition to child-process concurrency. On shutdown, the server waits up to 15 seconds for in-flight requests before canceling remaining requests and killing remaining process groups.

Ledger integers retain numeric JSON values within JavaScript's safe integer range. Larger integers use exact decimal strings, avoiding rounded amounts. Maintenance-authority counters retain the numeric schema consumed by Go.

## Operational logs

HTTP requests emit JSON lines to stdout, visible in Pod logs. `request_started` and
`request_completed` share a `request_id`, also returned as `X-Request-ID`. An incoming
ID is reused only if it is 1–64 characters from `A-Za-z0-9._:-`; otherwise the service
generates one. Unknown paths are logged as `unmatched`.

Completion logs include the method, path, contract address, effective network,
HTTP status, response bytes, duration, and outcome. State reads include
`state_mode` (`cma` or `ledger`), `block_hash` when provided, `state_read_queue_ms`,
and `state_read_run_ms`. Successful state reads need only the two request records;
there is no periodic memory polling or response buffering for logging.

Verification logs also identify compiler version, source type, source byte count,
contract-lock wait, compile queue/run times, and `verified_status`. An HTTP 200
verification mismatch is logged as a failed business result. Optional ABI/viewer/CMA
failures emit a `request_warning` without turning successful verification into failure.

Failed operations emit `operation_completed` with the operation, waiting/running
phase, queue/run times, safe `error_code`, and subprocess exit code when available.
Indexer and RPC failures distinguish HTTP (including `upstream_status`), protocol, network,
invalid responses, limits, and state decoding/not-found errors. Cancellation and
deadlines are explicit. Filter by `request_id` to connect these records. Startup
and shutdown use `server_started`, `server_stopping`, `server_shutdown_timeout`,
and `server_stopped`.

Logs exclude request/response bodies, source code, verification keys, authorization
headers, raw queries, upstream URLs, and free-form subprocess/provider diagnostics.
Selected user-controlled metadata is capped at 128 safe characters. Existing
low-level filesystem/CLI diagnostics retain their prior text format.

## HTTP API
1) POST /verify — verify a contract and read its public metadata
- Request body
```json
{
  "contract_addr": "0x...",
  "source_code": "<source or standard-input-json>",
  "compiler_version": "0.26.0",
  "verification_keys": {"<circuit>": "<hash>"},
  "source_type": "single_file",
  "networkid": "preview"
}
```
- source_type: "single_file" | "multi_file" (multi_file expects valid Compact standard-input-json with `contracts` and `entry-file`; the embedded JSON must itself be valid, e.g. no trailing commas)
- networkid/network_id: optional, supported values are preview/testnet-02/devnet/mainnet/preprod
- Success returns: verified_status, abi, viewer, cma (committee)
- verified_status is `perfect` when every on-chain circuit matches the compiled source
- verified_status is `partial` when every compiled circuit matches and the chain has additional circuits. `uncovered_circuits` lists those circuit names. A compiled circuit whose key does not match is `mismatch`

2) GET /contract/state — read contract public state
- Query params: `view=cma` optionally skips ledger decoding even for verified contracts and returns maintenance authority and operations. contract_addr=0x..., block_hash=optional 0x-prefixed 32-byte hash, networkid/network_id=optional. The legacy `hash` parameter is accepted; `block_hash` takes precedence. Historical offsets apply to verified ledger reads and unverified maintenance-authority/verifier-key reads.
- Mainnet and Preprod use native Node RPC block snapshots, including blocks without contract actions. Reads without a hash use the finalized head; historical reads require the node to retain the requested state. An empty snapshot or missing block returns HTTP 404. Provider/decoding errors return 502 and deadlines return 504. Historical failures never fall back to latest. Responses contain no provider credentials or free-form provider errors.
- Other networks query `contractAction`, not the beta `contract` snapshot field. Preview checks found a successful action-block read but a null result for a later block without a contract action. Arbitrary-block snapshot coverage on those Indexers is not established. A missing Indexer action returns 502 (`indexer_state_unavailable` in logs), not a claim that the contract is absent.
- Examples: `/contract/state?contract_addr=0x...` or `/contract/state?contract_addr=0x...&block_hash=0x...&networkid=preview`

### cURL examples
- Verify
```bash
curl -X POST http://localhost:8080/verify \
  -H 'Content-Type: application/json' \
  -d '{
    "contract_addr": "0x...",
    "source_code": "contract ...",
    "compiler_version": "0.26.0",
    "verification_keys": {"increment": "<hash>"},
    "source_type": "single_file",
    "networkid": "preview"
  }'
```
- Read state
```bash
curl 'http://localhost:8080/contract/state?contract_addr=0x...&networkid=preview'
```

## Supported Compact versions
Defined in code (compile/compile.go: CompactVersions) and can be pre-installed via `./midnight-server download`:
- 0.22.0, 0.23.0, 0.24.0, 0.25.0, 0.26.0, 0.28.0, 0.29.0, 0.30.0, 0.31.0, 0.31.1
- Public-state readers support runtime declarations 0.14.0 (Compact 0.28/0.29), 0.15.0 (Compact 0.30) and 0.16.0 (Compact 0.31.x). Source verification support does not imply a reader exists for every older compiler.
- Mainnet and Preview add Compact 0.36.0 with pinned runtime 0.21.0. ZKIR v3 is mandatory in 0.36.0, so do not send the obsolete feature flag. Stagenet retains opt-in 0.35.0; Preprod remains on its Ledger 8 compilers. Historical 0.28–0.31 verification remains available for existing artifacts.
- Generated readers resolve their declared runtime from the service installation, including artifacts stored on an external volume.
- Keep `package-lock.json` and `yarn.lock` in sync when changing dependencies: tests use npm, while Docker uses Yarn's frozen lockfile. Compatibility CI checks both installation paths.
- For real compiler/HTTP regression: `RUN_COMPACT_035_TESTS=1 RUN_COMPACT_036_TESTS=1 go test -mod=mod -v ./...`. The 0.36 test runs source compilation, independently generated reference-key comparison, persistence, ABI, and verified ledger reads on local providers. It does not deploy a funded on-chain contract.

## Testing
```bash
source ~/.gvm/scripts/gvm && gvm use go1.26.10
export GOTOOLCHAIN=local
npm ci
npm run test:state
compact update 0.29.0
compact update 0.31.1
go test -mod=mod -v ./...
go build ./...
go vet ./...
go build -o e2e/.midnight-server ./cmd
node --test e2e/http-cancellation.test.mjs e2e/preprod-rpc.test.mjs
```

Compatibility CI runs the Go, Compact, Node and HTTP regression checks on PRs
and pushes to `main`, using the Go compiler from `go.mod`.

Compile → deploy/call on **Preview** → verify → read latest and historical
action-block state (seven assertions; needs a funded Preview seed or mnemonic):

```bash
bash e2e/run.sh
```

See [E2E instructions](e2e/README.md). The runner uses an isolated local helper
and proof server with public Preview RPC/Indexer. Its wallet-free HTTP cancellation
test also runs in CI and verifies that client cancellation closes the reader's
Indexer connection. Production service deployment is separate.

State tests use real generated Compact 0.29/0.31.1 fixtures and local GraphQL / HTTP and WebSocket RPC providers. They require no funded wallet. Full Go tests require installed Compact 0.29/0.31.1 compilers. Wallet-free real-helper HTTP, historical-state and cancellation checks run with:

```bash
source ~/.gvm/scripts/gvm && gvm use go1.26.10
export GOTOOLCHAIN=local
go build -o e2e/.midnight-server ./cmd
node --test e2e/http-cancellation.test.mjs e2e/preprod-rpc.test.mjs
```

The opt-in configured-RPC acceptance is documented in [Preprod RPC](docs/preprod-rpc.md). It requires a real Preprod project ID for Blockfrost; custom-node acceptance does not establish Blockfrost historical-state coverage. Preview contract reads and authenticated Mainnet provider access remain separate deployment gates.

Compare the previous TypeScript reader with the fixed entry point using identical serialized runtime state and a local GraphQL server:

```bash
npm run bench:reader
# Optional: increase the number of cold processes per mode (2–100).
READER_BENCH_RUNS=20 npm run bench:reader
```

The benchmark checks identical JSON output and reports median process duration, time before the HTTP request, time after the response, and warm decode/ledger costs. It excludes production network latency and the old script-writing overhead. See [reader performance](docs/reader-performance.md) for measured results and scope.

## License
Apache 2.0

## Memory comparison

`e2e/memory-bench.mjs` compares two saved source trees using identical dependencies, fixtures, requests and Go toolchain. Build `midnight-server` for Linux/amd64 and `midnight-server-native` for the host in each tree. Build the shared reader image with `docker build --platform linux/amd64 -f e2e/memory.Dockerfile -t midnight-go:memory-bench .`, then run:

```sh
MEMORY_BENCH_BEFORE=/absolute/before MEMORY_BENCH_AFTER=/absolute/after \
MEMORY_BENCH_OUTPUT=/absolute/results.json \
node --import ./static/register-runtime.mjs e2e/memory-bench.mjs
```

The default is three runs per revision, alternating order. Linux read tests use fresh containers limited to 2 CPU / 4 GiB and report cgroup v2 memory.peak, including the Go server, child readers and accounted file cache. The 8 MiB ledger fixture uses valid official-runtime serialized state. Native mixed tests include a real Compact 0.31.1 compile and sample the Go/descendant RSS sum; this is a separate metric that can double-count shared pages. Burst results include admitted/rejected request counts so overload rejection cannot be mistaken for equal-throughput memory savings. On Apple Silicon the Linux/amd64 container is emulated; results are controlled comparisons, not production measurements. No production OOM resolution is established by this benchmark alone.
