#!/usr/bin/env bash
# Compile Compact 0.31.1, deploy to Midnight preview, then verify via midnight-go.
# Does not start a local midnight-node. Preview RPC/indexer are public.
# Requires a funded preview wallet: MIDNIGHT_PREVIEW_SEED or MIDNIGHT_PREVIEW_MNEMONIC.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
E2E="$ROOT/e2e"
cd "$E2E"

if ! command -v compact >/dev/null; then
  echo "compact CLI is required (install 0.31.1)" >&2
  exit 1
fi

ENV_FILE="${MIDNIGHT_PREVIEW_ENV_FILE:-.env.preview}"
if [[ -f "$ENV_FILE" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "$ENV_FILE"
  set +a
fi

if [[ -z "${MIDNIGHT_PREVIEW_SEED:-}" && -z "${MIDNIGHT_PREVIEW_MNEMONIC:-}" ]]; then
  echo "Set MIDNIGHT_PREVIEW_SEED or MIDNIGHT_PREVIEW_MNEMONIC (funded on preview)." >&2
  echo "Faucet: https://midnight-tmnight-preview.nethermind.dev/" >&2
  echo "Copy e2e/.env.preview.example to e2e/.env.preview" >&2
  exit 1
fi

echo "==> compile hello-world with Compact 0.31.1"
compact compile +0.31.1 contracts/hello-world.compact contracts/managed/hello-world

echo "==> yarn install"
yarn install --frozen-lockfile --ignore-engines

SERVER_PID=""
WORK_DIR=""
cleanup() {
  docker compose down || true
  if [[ -n "${SERVER_PID:-}" ]]; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  if [[ -n "${WORK_DIR:-}" ]]; then
    rm -rf "$WORK_DIR"
  fi
}
trap cleanup EXIT

export COMPOSE_PROJECT_NAME="midnight-reader-e2e-$$"
echo "==> start local proof-server only (preview node/indexer are public)"
yarn proof:up

echo "==> build and start midnight-go (preview RPC)"
(cd "$ROOT/cmd" && go build -o "$E2E/.midnight-server" .)
node --test "$E2E/http-cancellation.test.mjs"
SDK_PORT="${MIDNIGHT_SDK_PORT:-18080}"
WORK_DIR=$(mktemp -d "$ROOT/.e2e-reader-XXXXXX")
mkdir -p "$WORK_DIR/static" "$WORK_DIR/tmp"
for asset in state.mjs read-state.mjs register-runtime.mjs runtime-loader.mjs; do
  cp "$ROOT/static/$asset" "$WORK_DIR/static/$asset"
done
export STATIC_DIR="$WORK_DIR/static"
STATIC_DIR="$STATIC_DIR" TEMP_DIR="$WORK_DIR/tmp" NETWORK_ID=preview PORT="$SDK_PORT" \
  RPC_NODE=https://rpc.preview.midnight.network \
  INDEXER_RPC=https://indexer.preview.midnight.network/api/v4/graphql \
  INDEXER_WS=wss://indexer.preview.midnight.network/api/v4/graphql/ws \
  "$E2E/.midnight-server" &
SERVER_PID=$!
READY=false
for i in $(seq 1 50); do
  # An empty request returns 400 before running a Node reader. A probe contract
  # would invoke GraphQL and report 500 even when the service is healthy.
  if [[ "$(curl -sS -o /dev/null -w '%{http_code}' --max-time 1 "http://127.0.0.1:${SDK_PORT}/contract/state" 2>/dev/null || true)" == 400 ]]; then
    READY=true
    break
  fi
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "midnight-go failed to listen on :${SDK_PORT}" >&2
    exit 1
  fi
  sleep 0.2
done
if [[ "$READY" != true ]]; then
  echo "midnight-go did not become ready on :${SDK_PORT}" >&2
  exit 1
fi

echo "==> deploy to preview + verify"
export MIDNIGHT_NETWORK=preview
export MIDNIGHT_SDK_URL="http://127.0.0.1:${SDK_PORT}"
yarn test:preview
echo "==> e2e ok"
