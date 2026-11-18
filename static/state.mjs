import { ContractState } from '@midnight-ntwrk/compact-runtime';
import { ContractState as ContractStateV9 } from '@midnight-ntwrk/compact-runtime-0.21';
import { setTimeout as delay } from 'node:timers/promises';
import { setNetworkId } from '@midnight-ntwrk/midnight-js-network-id';
import { contractRuntime } from './runtime-loader.mjs';

function readerError(code, message, status) {
  return Object.assign(new Error(message), { diagnosticCode: code, upstreamStatus: status });
}

// Only fixed classifications cross the process boundary. Provider errors may
// contain credentials, GraphQL data, or complete request URLs.
export function readerDiagnostic(error) {
  const codes = new Set(['indexer_response_limit', 'indexer_invalid_response',
    'indexer_network_failed', 'indexer_http_failed', 'indexer_graphql_failed',
    'rpc_response_limit', 'rpc_invalid_response', 'rpc_network_failed',
    'rpc_http_failed', 'rpc_call_failed', 'rpc_invalid_request',
    'rpc_config_invalid', 'rpc_block_not_found',
    'state_decode_failed', 'state_not_found', 'indexer_state_unavailable', 'state_output_limit', 'deadline_exceeded']);
  const code = codes.has(error?.diagnosticCode) ? error.diagnosticCode
    : ['AbortError', 'TimeoutError'].includes(error?.name) ? 'deadline_exceeded' : 'state_read_failed';
  const result = { error_code: code };
  if (['indexer_http_failed', 'rpc_http_failed'].includes(code) && Number.isInteger(error.upstreamStatus)
    && error.upstreamStatus >= 400 && error.upstreamStatus <= 599) {
    result.upstream_status = error.upstreamStatus;
  }
  return result;
}

// WASM objects cannot be shared between runtime instances. Convert through the
// structural state encoding before calling a legacy contract's ledger reader.
export async function compiledLedger(moduleURL, ledger) {
  const runtime = await contractRuntime(moduleURL);
  return state => {
    const value = typeof state.encode === 'function' ? state : state.state;
    return ledger(value instanceof runtime.StateValue ? state : runtime.StateValue.decode(value.encode()));
  };
}

// Endpoints come from the validated Go request configuration, including provider
// credentials. Never reconstruct URLs from network names inside this process.
const contractStateQuery = `query CONTRACT_STATE_QUERY($address: HexEncoded!, $offset: ContractActionOffset) {
  contractAction(address: $address, offset: $offset) { state transaction { block { hash height } } }
}`;

function positiveEnv(name, fallback) {
  const value = Number(process.env[name]);
  return Number.isSafeInteger(value) && value > 0 ? value : fallback;
}

export async function boundedJSON(response, limit, provider = 'indexer') {
  if (Number(response.headers.get('content-length')) > limit) {
    await response.body?.cancel();
    throw readerError(`${provider}_response_limit`, 'Provider response exceeds configured limit');
  }
  const reader = response.body?.getReader();
  if (!reader) throw readerError(`${provider}_invalid_response`, 'Empty provider response');
  const chunks = [];
  let length = 0;
  try {
    for (;;) {
      const {done, value} = await reader.read();
      if (done) break;
      length += value.byteLength;
      if (length > limit) throw readerError(`${provider}_response_limit`, 'Provider response exceeds configured limit');
      chunks.push(value);
    }
    try {
      return JSON.parse(Buffer.concat(chunks, length).toString('utf8'));
    } catch {
      throw readerError(`${provider}_invalid_response`, 'Invalid provider JSON response');
    }
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}

export function decodeState(raw) {
  if (raw == null || raw === '') return null;
  if (typeof raw !== 'string' || raw.length % 2 || !/^[0-9a-f]+$/i.test(raw)) {
    throw readerError('state_decode_failed', 'Malformed contract state');
  }
  try {
    return decodeContractState(Buffer.from(raw, 'hex'));
  } catch {
    throw readerError('state_decode_failed', 'Contract state decode failed');
  }
}

function rpcResult(response, id) {
  if (!response || Array.isArray(response) || response.jsonrpc !== '2.0'
    || response.id !== id || Object.hasOwn(response, 'error') === Object.hasOwn(response, 'result')) {
    throw readerError('rpc_invalid_response', 'Invalid RPC response envelope');
  }
  if (Object.hasOwn(response, 'error')) {
    // Node messages can contain credentials, runtime traces or state payloads.
    throw readerError('rpc_call_failed', 'Contract state RPC call failed');
  }
  return response.result;
}

// Each state read owns one connection. Its finalized-head lookup and state query
// share the socket, and maxPayload rejects large frames before retaining them.
async function websocketRPC(endpoint, signal, limit) {
  const { default: WebSocket } = await import('ws');
  signal.throwIfAborted();
  const socket = new WebSocket(endpoint, { maxPayload: limit, perMessageDeflate: false });
  let pending, failure, nextID = 0;
  let resolveOpen, rejectOpen;
  const opened = new Promise((resolve, reject) => { resolveOpen = resolve; rejectOpen = reject; });
  const fail = error => {
    if (failure) return;
    failure = error;
    rejectOpen(error);
    pending?.reject(error);
    pending = undefined;
    socket.terminate();
  };
  const abort = () => fail(signal.reason);
  socket.on('open', resolveOpen);
  socket.on('error', error => fail(readerError(
    error.code === 'WS_ERR_UNSUPPORTED_MESSAGE_LENGTH' ? 'rpc_response_limit' : 'rpc_network_failed',
    'Contract state RPC connection failed',
  )));
  socket.on('close', () => fail(readerError('rpc_network_failed', 'Contract state RPC connection closed')));
  socket.on('unexpected-response', (_request, response) => {
    response.destroy();
    fail(readerError('rpc_http_failed', 'Contract state RPC handshake failed', response.statusCode));
  });
  socket.on('message', (data, binary) => {
    if (!pending) return;
    if (binary || data.length > limit) {
      fail(readerError(data.length > limit ? 'rpc_response_limit' : 'rpc_invalid_response', 'Invalid RPC message'));
      return;
    }
    let response;
    try { response = JSON.parse(data.toString('utf8')); }
    catch { fail(readerError('rpc_invalid_response', 'Invalid RPC JSON response')); return; }
    const current = pending;
    pending = undefined;
    try { current.resolve(rpcResult(response, current.id)); }
    catch (error) { current.reject(error); }
  });
  signal.addEventListener('abort', abort, { once: true });
  // Abort may have raced with listener registration.
  if (signal.aborted) abort();
  try { await opened; }
  catch (error) { signal.removeEventListener('abort', abort); throw error; }
  return {
    request(method, params) {
      signal.throwIfAborted();
      if (failure) throw failure;
      return new Promise((resolve, reject) => {
        const id = ++nextID;
        pending = { id, resolve, reject };
        socket.send(JSON.stringify({ jsonrpc: '2.0', id, method, params }), error => {
          if (error) fail(readerError('rpc_network_failed', 'Contract state RPC send failed'));
        });
      });
    },
    close() {
      signal.removeEventListener('abort', abort);
      socket.terminate();
    },
  };
}

async function httpRPC(endpoint, method, params, id, signal, limit, fetchImpl, retryDelay) {
  for (let attempt = 0; attempt < 5; attempt++) {
    let response;
    try {
      response = await fetchImpl(endpoint, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, signal,
        body: JSON.stringify({ jsonrpc: '2.0', id, method, params }),
      });
    } catch {
      signal.throwIfAborted();
      if (attempt === 4) throw readerError('rpc_network_failed', 'Contract state RPC connection failed');
    }
    if (response?.ok) return rpcResult(await boundedJSON(response, limit, 'rpc'), id);
    if (response) {
      await response.body?.cancel();
      if ((response.status !== 429 && response.status < 500) || attempt === 4) {
        throw readerError('rpc_http_failed', 'Contract state RPC HTTP request failed', response.status);
      }
    }
    await delay(Math.min(10000, retryDelay * 2 ** attempt) * (0.5 + Math.random()), undefined, { signal });
  }
}

// Mainnet and Preprod read native snapshots, including blocks without contract
// actions. The requested hash is never replaced by the latest action or head.
function rpcStateProvider({ fetchImpl = fetch, retryDelay = 1000 } = {}) {
  const endpoint = process.env.RPC_NODE;
  let protocol;
  try { protocol = new URL(endpoint).protocol; } catch { /* Report only a fixed classification below. */ }
  if (!['http:', 'https:', 'ws:', 'wss:'].includes(protocol)) {
    throw readerError('rpc_config_invalid', 'Contract snapshots require a configured RPC node');
  }
  return { async queryContractState(address, config, withSnapshot = false) {
    address = address.replace(/^0x/, '');
    if (!/^[0-9a-f]{64}$/i.test(address)) throw readerError('rpc_invalid_request', 'Invalid contract address');
    let hash;
    if (config?.type === 'blockHash') {
      hash = '0x' + config.blockHash.replace(/^0x/, '');
      if (!/^0x[0-9a-f]{64}$/i.test(hash)) throw readerError('rpc_invalid_request', 'Invalid block hash');
    } else if (config && (config.type !== 'blockHeight' || !Number.isSafeInteger(config.blockHeight) || config.blockHeight < 0)) {
      throw readerError('rpc_invalid_request', 'Invalid block offset');
    }
    const signal = AbortSignal.timeout(Math.min(2147483647, positiveEnv('MIDNIGHT_STATE_TIMEOUT_MS', 45000)));
    const limit = positiveEnv('MIDNIGHT_RPC_RESPONSE_LIMIT_BYTES', 32 << 20);
    let connection, nextID = 0;
    try {
      if (protocol === 'ws:' || protocol === 'wss:') connection = await websocketRPC(endpoint, signal, limit);
      const rpc = (method, params = []) => connection ? connection.request(method, params)
        : httpRPC(endpoint, method, params, ++nextID, signal, limit, fetchImpl, retryDelay);
      if (!hash) {
        hash = config ? await rpc('chain_getBlockHash', [config.blockHeight]) : await rpc('chain_getFinalizedHead');
        if (hash === null) throw readerError('rpc_block_not_found', 'Requested block was not found');
        if (typeof hash !== 'string' || !/^0x[0-9a-f]{64}$/i.test(hash)) {
          throw readerError('rpc_invalid_response', 'Invalid RPC block hash');
        }
      }
      const raw = await rpc('midnight_contractState', [address, hash]);
      if (typeof raw !== 'string') throw readerError('rpc_invalid_response', 'Invalid RPC contract state');
      const state = decodeState(raw.replace(/^0x/, ''));
      return withSnapshot ? { state, snapshot: { block_hash: hash.toLowerCase() } } : state;
    } finally {
      connection?.close();
    }
  }};
}

// Select only the state transport; both providers use the same official decoder
// without loading the SDK's subscription, transaction, Apollo or RxJS layers.
export function stateProvider({fetchImpl = fetch, retryDelay = 1000} = {}) {
  const { MIDNIGHT_NETWORK_ID, INDEXER_RPC } = process.env;
  if (MIDNIGHT_NETWORK_ID === 'mainnet' || MIDNIGHT_NETWORK_ID === 'preprod') {
    setNetworkId(MIDNIGHT_NETWORK_ID);
    return rpcStateProvider({ fetchImpl, retryDelay });
  }
  if (!MIDNIGHT_NETWORK_ID || !INDEXER_RPC || !['http:', 'https:'].includes(new URL(INDEXER_RPC).protocol)) {
    throw new Error('Contract reader requires network ID and an HTTP Indexer endpoint');
  }
  setNetworkId(MIDNIGHT_NETWORK_ID);
  return { async queryContractState(address, config, withSnapshot = false) {
    const offset = config ? {blockOffset: config.type === 'blockHeight' ? {height: config.blockHeight} : {hash: config.blockHash}} : null;
    const signal = AbortSignal.timeout(Math.min(2147483647, positiveEnv('MIDNIGHT_STATE_TIMEOUT_MS', 45000)));
    const limit = positiveEnv('MIDNIGHT_INDEXER_RESPONSE_LIMIT_BYTES', 32 << 20);
    for (let attempt = 0; attempt < 5; attempt++) {
      let response;
      let retry = false;
      try {
        response = await fetchImpl(INDEXER_RPC, {
          method: 'POST', headers: {'Content-Type': 'application/json'}, signal,
          body: JSON.stringify({query: contractStateQuery, variables: {address, offset}}),
        });
      } catch (error) {
        if (signal.aborted || attempt === 4) {
          throw readerError(signal.aborted ? 'deadline_exceeded' : 'indexer_network_failed', error.message);
        }
        retry = true;
      }
      if (response && !response.ok) {
        retry = response.status === 429 || response.status >= 500;
        await response.body?.cancel();
        if (!retry || attempt === 4) throw readerError('indexer_http_failed', 'Indexer HTTP query failed', response.status);
      }
      if (retry) {
        await delay(Math.min(10000, retryDelay * 2 ** attempt) * (0.5 + Math.random()), undefined, {signal});
        continue;
      }
      const result = await boundedJSON(response, limit);
      if (result.errors?.length) throw readerError('indexer_graphql_failed', 'Indexer GraphQL query failed');
      const raw = result.data?.contractAction?.state;
      if (raw == null) throw readerError('indexer_state_unavailable', 'Indexer has no contract action at the requested offset');
      const state = decodeState(raw);
      if (!withSnapshot) return state;
      const block = result.data.contractAction.transaction?.block;
      const hash = config?.type === 'blockHash' ? config.blockHash : block?.hash;
      if (typeof hash !== 'string' || !/^(?:0x)?[0-9a-f]{64}$/i.test(hash)) {
        throw readerError('indexer_invalid_response', 'Contract state block is unavailable');
      }
      return { state, snapshot: { block_hash: '0x' + hash.replace(/^0x/i, '').toLowerCase(),
        ...(config ? {} : { block_height: block.height }) } };
    }
  }};
}

// Serialization versions belong to the state type, not the network or chain head.
// Historical Ledger 8 state remains readable after an upgrade to Ledger 9.
export function decodeContractState(raw) {
  const bytes = Buffer.from(raw);
  if (bytes.subarray(0, 28).toString().startsWith('midnight:contract-state[v6]:')) return ContractState.deserialize(bytes);
  if (bytes.subarray(0, 28).toString().startsWith('midnight:contract-state[v8]:')) return ContractStateV9.deserialize(bytes);
  throw readerError('state_decode_failed', 'Unsupported contract state serialization version');
}

export async function readContractState(address, hash, provider = stateProvider()) {
  const offset = hash ? { type: 'blockHash', blockHash: hash.replace(/^0x/, '') } : undefined;
  return provider.queryContractState(address, offset);
}

// The returned block identifies exactly the state shown to the user, so a later
// proof request can read the same snapshot even after the contract changes.
export async function readContractSnapshot(address, hash, provider = stateProvider()) {
  const offset = hash ? { type: 'blockHash', blockHash: hash.replace(/^0x/, '') } : undefined;
  return provider.queryContractState(address, offset, true);
}

export function stateResult(state, ledger) {
  if (state === null) throw readerError('state_not_found', 'Contract state not found at the requested block');
  const counter = Number(state.maintenanceAuthority.counter);
  if (!Number.isSafeInteger(counter)) throw new Error('Maintenance counter exceeds the JSON numeric range');
  return {
    maintenanceAuthority: {
      committee: state.maintenanceAuthority.committee,
      threshold: state.maintenanceAuthority.threshold,
      counter,
    },
    operations: state.operations().map(operation => {
      const details = state.operation(operation);
      return {
        operation: typeof operation === 'string' ? operation : Buffer.from(operation).toString('hex'),
        verifierKey: details?.verifierKey ? Buffer.from(details.verifierKey).toString('hex') : null,
      };
    }),
    ...(ledger ? { data: ledger(state.data) } : {}),
  };
}

export function replacer(_key, value) {
  if (typeof value === 'bigint') {
    const number = Number(value);
    return Number.isSafeInteger(number) ? number : value.toString();
  }
  if (value instanceof Uint8Array) return Buffer.from(value).toString('hex');
  if (value instanceof Set) return Array.from(value);
  if (value instanceof Map) return Object.fromEntries(value);
  if (value && typeof value.root === 'function') return value.root().field.toString(16);
  return value;
}

// Count the exact UTF-8 JSON representation while traversing, before JSON.stringify
// can allocate an oversized final string. Go also enforces the output byte limit.
export function stringifyState(value, limit = positiveEnv('MIDNIGHT_STATE_OUTPUT_LIMIT_BYTES', 16 << 20)) {
  let bytes = 0, root = true;
  const parents = new WeakSet();
  const add = count => {
    bytes += count;
    if (bytes > limit) throw readerError('state_output_limit', 'Contract state JSON exceeds configured limit');
  };
  const quoted = text => {
    // Reject large strings before allocating their escaped representation.
    if (Buffer.byteLength(text) + 2 > limit - bytes) throw readerError('state_output_limit', 'Contract state JSON exceeds configured limit');
    add(Buffer.byteLength(JSON.stringify(text)));
  };
  return JSON.stringify(value, function(key, original) {
    const result = replacer(key, original);
    const omitted = result === undefined || typeof result === 'function' || typeof result === 'symbol';
    if (root) root = false;
    else if (Array.isArray(this)) {
      if (key !== '0') add(1);
      if (omitted) { add(4); return result; }
    } else {
      if (omitted) return result;
      if (parents.has(this)) add(1);
      parents.add(this);
      quoted(key); add(1);
    }
    if (typeof result === 'string') quoted(result);
    else if (result !== null && typeof result === 'object') {
      parents.delete(result); // A shared object may be serialized again in another branch.
      add(2);
    }
    else if (!omitted) add(JSON.stringify(result).length);
    return result;
  });
}
