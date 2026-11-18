# Contract reader E2E results

Verified on 2026-10-03 (Asia/Shanghai), with Go 1.25.3, Node 22,
Compact 0.31.1, Midnight JS 4.1.1, wallet SDK 1.2.0 and proof-server 8.1.0.

## Real Preview transactions: 7 passed

The test compiled Hello World, started an isolated local Go helper and proof
server, and used the official Preview RPC and Indexer v4. It reused an existing
funded test wallet through an external ignored environment file.

| Assertion | Result |
| --- | --- |
| Deploy and read the initial empty message through the official SDK | Passed |
| Read unverified authority and compare HTTP verifier bytes with the compiler key | Passed |
| Call `storeMessage` and read `Hello World!` through the official SDK | Passed |
| Verify source through HTTP `/verify`, including viewer and committee | Passed (`perfect`) |
| Read latest verified state through HTTP; preserve authority/keys and create no generated reader | Passed |
| Read distinct deployment/call block states; preserve legacy `hash` and `block_hash` precedence | Passed |
| Reject malformed block hashes through HTTP | Passed (400) |

Public receipt:

```json
{
  "contractAddress": "59e687c6da008db0d5ac3f7ccf8f9daf3c256712d430da670ea3979bf4e2dbe7",
  "compilerVersion": "0.31.1",
  "deployBlockHash": "0x731769c061fa0eaa6a88d7b55c0da5dafbdecb7e54e0a13d3c77dc98b6844999",
  "callBlockHash": "0x317184438b718e1590d84c3e8857e5e7e40e39c47c100ca55b6e325a0a39a400"
}
```

The successful suite took 580.43 seconds, including initial wallet sync. An
earlier attempt reached its deliberately short 480-second sync limit and skipped
all seven assertions; the successful attempt used a 20-minute sync limit and a
25-minute setup/cleanup limit.

The runner file was edited while that successful suite was running, causing an
EOF diagnostic after Vitest reported seven passes. Its cleanup completed. The
settled script passed `bash -n`, and a subsequent launcher smoke test exercised
compilation, proof-server startup, the real helper, cancellation and cleanup,
substituting a type check for the already-passed transaction suite. The smoke
run completed normally without submitting another transaction. The updated
private-state directory was also checked with the real LevelDB provider by
writing, reading and clearing an isolated fixture.

## HTTP cancellation and compatibility

`node --test e2e/http-cancellation.test.mjs`: **1 passed** locally and in Linux
CI. It starts the real helper binary against a stalled local Indexer, cancels
the HTTP client, observes the upstream connection close, and checks that the
helper remains responsive. No wallet or chain transaction is needed.

An additional check on an existing Preview contract
`39ec5020f9da122c9a15a9b6c0c8db290ccbf15559d1b71f16ddab858a424cbe`
passed five HTTP checks: unverified metadata/key equality, source verification,
verified state at action block 885432, latest state, and the legacy hash
parameter. It returned `message: "Hello World!"`.

CI passed Go tests/build/vet, six Node regressions, HTTP cancellation and six
tests inside the frozen-Yarn Linux image.
The E2E TypeScript check passed after generating the contract.

## Reproduce

```bash
source ~/.gvm/scripts/gvm && gvm use go1.26.10
export GOTOOLCHAIN=local
MIDNIGHT_PREVIEW_ENV_FILE=/absolute/path/to/.env.preview \
MIDNIGHT_SYNC_TIMEOUT_MS=1200000 \
MIDNIGHT_E2E_HOOK_TIMEOUT_MS=1500000 \
bash e2e/run.sh
```

For wallet-free validation:

```bash
source ~/.gvm/scripts/gvm && gvm use go1.26.10
export GOTOOLCHAIN=local
npm run test:state
go test -mod=mod -v ./...
go build ./...
go vet ./...
go build -o e2e/.midnight-server ./cmd
node --test e2e/http-cancellation.test.mjs
```

See [E2E setup](../e2e/README.md) for dependencies and wallet configuration.

## Scope

This verifies the helper's Preview contract path. It does not establish deployed
Subscan API/scanner acceptance, Preprod/Mainnet acceptance, Mainnet credentials,
production throughput or arbitrary-block snapshots. Historical assertions use
blocks containing actions for this contract; the SDK's `contractAction` query
still has the documented gap for unrelated blocks. No production service or
GitOps deployment was performed.
