# Compact compile → deploy → call → verify → read E2E

Uses **Midnight preview** (public RPC and indexer). Does not start a local `midnight-node`.

1. Compile `contracts/hello-world.compact` with Compact **0.31.1**
2. Start only a local **proof-server** (proving still needs a local prover)
3. Deploy with Midnight.js 4.1.1 to preview
4. Read unverified authority and verifier keys through the actual HTTP service
5. Call `storeMessage`, then POST source + verifier-key hashes to `/verify`
6. Assert latest public ledger state, unchanged authority/keys, and distinct
   deployment/call block states; check `hash` compatibility, `block_hash`
   precedence, and malformed-hash rejection

The seven Preview assertions use real transactions and public Indexer data.
Historical reads cover blocks containing a contract action; this suite does not
establish arbitrary-block snapshot coverage or Mainnet/Preprod readiness.
Recorded receipts and results are in [reader E2E results](../docs/reader-e2e-results.md).

The runner also tests HTTP client cancellation with an isolated stalled local
Indexer. It starts the real Go service and checks that canceling the HTTP request
closes the Node reader's upstream connection while the service stays responsive.
This wallet-free check runs in CI too.

Deploy/wallet code is adapted from [midnightntwrk/example-hello-world](https://github.com/midnightntwrk/example-hello-world) (Apache-2.0).

## Preview wallet

Copy `.env.preview.example` to `.env.preview` and set **one** of:

- `MIDNIGHT_PREVIEW_SEED` (even-length hex, no `0x`)
- `MIDNIGHT_PREVIEW_MNEMONIC`

Fund tNIGHT at https://midnight-tmnight-preview.nethermind.dev/ and wait for DUST.

## Run

```bash
source ~/.gvm/scripts/gvm && gvm use go1.26.10
export GOTOOLCHAIN=local
bash e2e/run.sh
```

To reuse a wallet file outside this checkout without copying credentials:

```bash
MIDNIGHT_PREVIEW_ENV_FILE=/absolute/path/to/.env.preview bash e2e/run.sh
```

`MIDNIGHT_SYNC_TIMEOUT_MS` bounds wallet sync (default 60 minutes on remote
networks); `MIDNIGHT_E2E_HOOK_TIMEOUT_MS` bounds setup/cleanup (default 90
minutes). Set the hook timeout above the sync timeout to allow DUST registration.
Credentials stay in the ignored wallet file/process environment; logs and
`e2e/.deployed.json` contain no seed or private transaction. The receipt records
only the contract address, compiler version, and deployment/call block hashes.

The runner uses isolated static/temp/private-state directories and a Compose project and stops
its service/proof server on exit. It binds the helper to port 18080 by default
(`MIDNIGHT_SDK_PORT` overrides it) and always targets that local helper. Preview
contract deployment spends test-wallet DUST; production services are untouched.

Wallet-free HTTP cancellation check (no proof server or on-chain deployment):

```bash
source ~/.gvm/scripts/gvm && gvm use go1.26.10
export GOTOOLCHAIN=local
go build -o e2e/.midnight-server ./cmd
node --test e2e/http-cancellation.test.mjs
```

Type check after compiling the generated contract:

```bash
cd e2e
yarn compile
yarn exec tsc --noEmit
```

Go-only compile check (no contract deployment):

```bash
source ~/.gvm/scripts/gvm && gvm use go1.26.10
export GOTOOLCHAIN=local
go test -mod=mod -v ./compile/ -run TestCompileHelloWorld0311
```
