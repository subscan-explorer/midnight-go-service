import { describe, it, expect, beforeAll, afterAll } from 'vitest';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { WebSocket } from 'ws';
import { setNetworkId } from '@midnight-ntwrk/midnight-js-network-id';
import {
  deployContract,
  submitCallTx,
  type DeployedContract,
} from '@midnight-ntwrk/midnight-js-contracts';
import type { ContractAddress } from '@midnight-ntwrk/midnight-js-protocol/compact-runtime';
import {
  type EnvironmentConfiguration,
  waitForFunds,
} from '@midnight-ntwrk/testkit-js';
import pino from 'pino';

import { getConfig } from '../config.js';
import {
  MidnightWalletProvider,
  syncWallet,
  type WalletSecret,
} from '../wallet.js';
import { buildProviders, type HelloWorldProviders } from '../providers.js';
import {
  CompiledHelloWorldContract,
  Contract,
  ledger,
  zkConfigPath,
} from '../../contracts/index.js';

// Required for GraphQL subscriptions in Node.js
// @ts-expect-error WebSocket global assignment for apollo
globalThis.WebSocket = WebSocket;

const ALICE_LOCAL_SEED =
  '0000000000000000000000000000000000000000000000000000000000000001';
const PRIVATE_STATE_ID = 'AlicePrivateHWState';

const logger = pino({
  level: process.env['LOG_LEVEL'] ?? 'info',
  transport: { target: 'pino-pretty' },
});

const network = process.env['MIDNIGHT_NETWORK'] ?? 'local';

function resolveSecret(net: string): WalletSecret {
  if (net === 'local') return { kind: 'seed', value: ALICE_LOCAL_SEED };

  const upper = net.toUpperCase();
  const mnemonicEnv = `MIDNIGHT_${upper}_MNEMONIC`;
  const seedEnv = `MIDNIGHT_${upper}_SEED`;
  const mnemonic = process.env[mnemonicEnv]?.trim().replace(/\s+/g, ' ');
  const seedHex = process.env[seedEnv]?.trim();

  if (mnemonic && seedHex) {
    throw new Error(
      `Set only one of ${mnemonicEnv} or ${seedEnv} (both are defined).`,
    );
  }
  if (mnemonic) {
    return { kind: 'mnemonic', value: mnemonic };
  }
  if (seedHex) {
    if (!/^[0-9a-fA-F]+$/.test(seedHex) || seedHex.length % 2 !== 0) {
      throw new Error(
        `${seedEnv} must be a hex string of even length (no 0x prefix).`,
      );
    }
    return { kind: 'seed', value: seedHex };
  }
  throw new Error(
    `Either ${mnemonicEnv} or ${seedEnv} is required for network '${net}'. ` +
      `Set one in .env.${net} or the shell.`,
  );
}

describe(`Hello World Contract (${network})`, () => {
  let wallet: MidnightWalletProvider;
  let providers: HelloWorldProviders;
  let contractAddress: ContractAddress;
  let deployBlockHash: string;
  let callBlockHash: string;
  let publicAuthority: unknown;
  let publicOperations: unknown;

  const config = getConfig();
  const secret = resolveSecret(network);
  const isRemote = network !== 'local';
  const syncTimeoutMs = Number(
    process.env['MIDNIGHT_SYNC_TIMEOUT_MS'] ??
      (isRemote ? 60 * 60_000 : 10 * 60_000),
  );

  async function readState(params: Record<string, string> = {}) {
    const sdk = process.env['MIDNIGHT_SDK_URL'] ?? 'http://127.0.0.1:8080';
    const url = new URL('/contract/state', sdk);
    url.search = new URLSearchParams({
      contract_addr: contractAddress,
      network_id: config.networkId,
      ...params,
    }).toString();
    const response = await fetch(url, { signal: AbortSignal.timeout(30_000) });
    const text = await response.text();
    expect(response.status, text).toBe(200);
    return JSON.parse(text);
  }

  async function queryLedger(p: HelloWorldProviders) {
    const state = await p.publicDataProvider.queryContractState(contractAddress);
    expect(state).not.toBeNull();
    return ledger(state!.data);
  }

  beforeAll(async () => {
    setNetworkId(config.networkId);

    const envConfig: EnvironmentConfiguration = {
      walletNetworkId: config.networkId,
      networkId: config.networkId,
      indexer: config.indexer,
      indexerWS: config.indexerWS,
      node: config.node,
      nodeWS: config.nodeWS,
      faucet: config.faucet,
      proofServer: config.proofServer,
    };

    wallet = await MidnightWalletProvider.build(logger, envConfig, secret);
    await wallet.start();
    await syncWallet(logger, wallet.wallet, syncTimeoutMs);

    if (isRemote) {
      // NIGHT→DUST registration. Seed is pre-funded via the faucet page; idempotent.
      const nightBalance = await waitForFunds(
        wallet.wallet,
        envConfig,
        false,
        wallet.unshieldedKeystore,
      );
      logger.info(`Wallet NIGHT balance on '${network}': ${nightBalance}`);
    }

    providers = buildProviders(wallet, zkConfigPath, config);
    logger.info(`Providers initialized on '${network}'. Ready to test!`);
  });

  afterAll(async () => {
    if (wallet) {
      logger.info('Stopping wallet...');
      await wallet.stop();
    }
  });

  it('Deploys the contract', async () => {
    logger.info(`Creating private state...`);

    const deployed: DeployedContract<Contract> =
      await (deployContract<Contract>)(providers, {
        compiledContract: CompiledHelloWorldContract,
        privateStateId: PRIVATE_STATE_ID,
        initialPrivateState: {},
      });

    logger.info(`Setting the contract address...`);
    contractAddress = deployed.deployTxData.public.contractAddress;
    deployBlockHash = `0x${deployed.deployTxData.public.blockHash.replace(/^0x/, '')}`;
    logger.info(`Contract deployed at: ${contractAddress}`);
    expect(contractAddress).toBeDefined();
    expect(contractAddress.length).toBeGreaterThan(0);

    const state = await queryLedger(providers);
    expect(state.message).toEqual('');
    // Save only public receipt fields, so failures after submission are traceable
    // without exposing private transaction or wallet material.
    fs.writeFileSync(path.join(zkConfigPath, '..', '..', '..', '.deployed.json'),
      JSON.stringify({ contractAddress, compilerVersion: '0.31.1', deployBlockHash }, null, 2));
  });

  it('Reads unverified authority and operation keys through the HTTP service', async () => {
    const state = await readState({ block_hash: deployBlockHash });
    expect(state.data).toBeUndefined();
    expect(state.maintenanceAuthority.committee.length).toBeGreaterThan(0);
    const verifier = fs.readFileSync(path.join(zkConfigPath, 'keys', 'storeMessage.verifier'));
    const operation = state.operations.find((entry: { operation: string }) => entry.operation === 'storeMessage');
    expect(operation).toBeDefined();
    expect(crypto.createHash('md5').update(Buffer.from(operation.verifierKey, 'hex')).digest('hex'))
      .toBe(crypto.createHash('md5').update(verifier).digest('hex'));
    publicAuthority = state.maintenanceAuthority;
    publicOperations = state.operations;
  });

  it('Stores Hello World!', async () => {
    const message = 'Hello World!';

    const called = await (submitCallTx<Contract, 'storeMessage'>)(providers, {
      compiledContract: CompiledHelloWorldContract,
      contractAddress,
      privateStateId: PRIVATE_STATE_ID,
      circuitId: 'storeMessage',
      args: [message],
    });
    callBlockHash = `0x${called.public.blockHash.replace(/^0x/, '')}`;

    const state = await queryLedger(providers);
    expect(state.message).toEqual(message);
  });

  it('Verifies compiled source against midnight-go /verify', async () => {
    const sdk = process.env['MIDNIGHT_SDK_URL'] ?? 'http://127.0.0.1:8080';
    const source = fs.readFileSync(
      path.join(zkConfigPath, '..', '..', 'hello-world.compact'),
      'utf8',
    );
    const verifier = fs.readFileSync(path.join(zkConfigPath, 'keys', 'storeMessage.verifier'));
    const hash = crypto.createHash('md5').update(verifier).digest('hex');

    fs.writeFileSync(
      path.join(zkConfigPath, '..', '..', '..', '.deployed.json'),
      JSON.stringify({ contractAddress, compilerVersion: '0.31.1', deployBlockHash, callBlockHash }, null, 2),
    );

    let res: Response;
    try {
      res = await fetch(`${sdk.replace(/\/$/, '')}/verify`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          contract_addr: contractAddress,
          source_code: source,
          compiler_version: '0.31.1',
          source_type: 'single_file',
          verification_keys: { storeMessage: hash },
          network_id: network === 'local' ? 'undeployed' : network,
        }),
        signal: AbortSignal.timeout(120_000),
      });
    } catch (err) {
      throw new Error(
        `midnight-go /verify unreachable at ${sdk}. Start ./midnight-server first. ${String(err)}`,
      );
    }
    const text = await res.text();
    expect(res.ok, text).toBe(true);
    const body = JSON.parse(text) as { verified_status?: string; message?: string; viewer?: { ledger: Record<string, string> }; cma?: unknown };
    expect(body.verified_status, body.message).toBe('perfect');
    expect(body.viewer?.ledger.message).toBe('Opaque<"string">');
    expect(body.cma).toEqual((publicAuthority as { committee: string[] }).committee);
  });

  it('Reads verified latest state and preserves authority and verifier keys', async () => {
    const state = await readState();
    expect(state.data).toEqual({ message: 'Hello World!' });
    expect(state.maintenanceAuthority).toEqual(publicAuthority);
    expect(state.operations).toEqual(publicOperations);
    const generated = path.join(process.env['STATIC_DIR'] ?? path.resolve('..', 'static'), 'verified', contractAddress, 'generated_demo.ts');
    expect(fs.existsSync(generated)).toBe(false);
  });

  it('Reads deployment state after the call without falling back to latest', async () => {
    const initial = await readState({ block_hash: deployBlockHash });
    expect(initial.data).toEqual({ message: '' });
    const called = await readState({ block_hash: callBlockHash });
    expect(called.data).toEqual({ message: 'Hello World!' });
    const legacy = await readState({ hash: deployBlockHash });
    expect(legacy).toEqual(initial);
    const precedence = await readState({ block_hash: deployBlockHash, hash: callBlockHash });
    expect(precedence).toEqual(initial);
  });

  it('Rejects malformed block hashes through the HTTP service', async () => {
    const sdk = process.env['MIDNIGHT_SDK_URL'] ?? 'http://127.0.0.1:8080';
    const url = new URL('/contract/state', sdk);
    url.search = new URLSearchParams({ contract_addr: contractAddress, block_hash: '0x1234' }).toString();
    const response = await fetch(url, { signal: AbortSignal.timeout(30_000) });
    expect(response.status).toBe(400);
    expect(await response.text()).toContain('Invalid block hash');
  });
});
