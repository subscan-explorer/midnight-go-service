# Midnight Go development

- Use Go `1.26.10`, matching `go.mod`, the Docker builder and CI. Before Go commands
  on this machine, source `~/.gvm/scripts/gvm` and run `gvm use go1.26.10`; do not use
  the broken `/usr/local/go/bin/go` installation.
- Set `GOTOOLCHAIN=local` during validation to reject implicit compiler upgrades.
- Install Node dependencies with `npm ci`. Full Go tests require Compact `0.29.0`
  and `0.31.1`; use the existing `compact` CLI and the installation instructions in
  `README.md`.
- Validate with `npm run test:state`, `go test -mod=mod -v ./...`, `go build ./...`
  and `go vet ./...`. Build `e2e/.midnight-server` from `./cmd`, then run
  `node --test e2e/http-cancellation.test.mjs e2e/preprod-rpc.test.mjs` for local
  HTTP/GraphQL/RPC regressions.
- Keep public-network acceptance and funded-wallet contract deployment opt-in;
  the default regression suite uses local providers and needs no chain writes.
- PR checks cover Go, Compact, Node and HTTP regressions. Kubernetes deployment
  changes belong in the GitOps repository when separately requested.
