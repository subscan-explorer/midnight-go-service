# Contract reader performance

Measured on 2026-10-03 (Asia/Shanghai), Node 22.16.0, macOS/arm64.

## Change

Keep the official Midnight JS/WASM decoder and generated contract ledger. One fixed `.mjs` entry point replaces generated TypeScript on the request path. Verified reads pass the existing compiled module path; unverified reads use the same entry point without that path. Module loading overlaps the GraphQL query. Existing generated files remain untouched, and legacy `.ts` execution remains available.

Requests still use isolated Node processes, preserving per-request network configuration and cancellation. No response cache, worker pool, dependency upgrade or additional Deployment is introduced. A persistent worker would need measured load/memory data, separate network isolation, bounded concurrency, cancellation and crash recovery before adoption.

## Local comparison

Command: `READER_BENCH_RUNS=20 npm run bench:reader`.

Both modes ran 20 independent processes after one warm-up per mode, with alternating order, identical Compact 0.31.1 output and a local GraphQL service returning 151 bytes of real serialized contract state. The baseline reproduces the previous generated reader and `ts-node` loader. It excludes the old per-request script writes. Every JSON response was compared for equality.

| Median | Previous reader | Fixed reader |
| --- | ---: | ---: |
| Process duration | 544.45 ms | 409.16 ms |
| Start to HTTP request | 523.79 ms | 387.40 ms |
| HTTP response to process completion | 22.35 ms | 22.09 ms |

Process duration decreased **24.8%** in this local experiment. Production speedup is unmeasured: remote latency, concurrency, CPU/memory limits and larger contract states can change the result. The last row includes decoding, serialization and process teardown; it is not a standalone decode measurement.

Separate warm in-process medians over 100 samples were 0.138 ms for official state deserialization and 0.491 ms for ledger conversion plus JSON serialization. These small-fixture measurements support reducing reader startup work first, without rewriting the ledger decoder.

## Correctness evidence

- `npm run test:state`: 6 tests passed, including real Compact 0.29/0.31.1 readers, latest/historical offsets, unverified reads, malformed/null state, unknown runtime rejection and credential-safe failures.
- `source ~/.gvm/scripts/gvm && gvm use go1.25.3 && go test -mod=mod -v ./... && go build ./... && go vet ./...`: passed. Go integration tests exercise real Node/GraphQL calls, unchanged compiled/generated files, canceled requests and termination while the Indexer is stalled. Existing opt-in live RPC/old-reader tests were skipped.
- Read-only Preview check at 2026-10-03 00:16:48 (Asia/Shanghai): contract `39ec5020f9da122c9a15a9b6c0c8db290ccbf15559d1b71f16ddab858a424cbe`, action block 885432, hash `5990e1ae56b55f97227a0e4f731cb44d1bb70ae4ba0de8a30e634cd9277af2d7`. The previous reader and fixed reader returned identical complete JSON, including `message: "Hello World!"`. The latest ledger matched, and the unverified path returned identical authority/operation keys.

The same live check found null state from the existing SDK at later block 1123114 (`5845167508b6cd2702a33b385ee4d46e0d226fb31efe68cf131dd74f1474dd48`), which has no action for this contract. This is a pre-existing query limitation; the optimization preserves the error. Arbitrary-block snapshot acceptance remains unfulfilled. The official [Indexer schema](https://raw.githubusercontent.com/midnightntwrk/midnight-indexer/main/indexer-api/graphql/schema-v4.graphql) distinguishes `contractAction` from the beta `contract(address, offset)` field with state at or before the block. A future snapshot adapter requires deployed-schema checks for each network; never silently substitute latest state.

No deployment, production load test, Mainnet authentication test or Preprod live contract read was performed in this optimization task. The draft PR workflow also runs reader subprocess tests inside its frozen-Yarn Linux review image.
