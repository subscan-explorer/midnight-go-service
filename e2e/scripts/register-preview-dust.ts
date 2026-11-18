import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { setNetworkId } from '@midnight-ntwrk/midnight-js-network-id';
import { waitForFunds } from '@midnight-ntwrk/testkit-js';
import { firstValueFrom } from 'rxjs';
import pino from 'pino';

import { PREVIEW_CONFIG } from '../src/config.js';
import { MidnightWalletProvider, syncWallet } from '../src/wallet.js';

const e2eDir = join(dirname(fileURLToPath(import.meta.url)), '..');
const envPath = join(e2eDir, '.env.preview');
const envText = readFileSync(envPath, 'utf8');
const seed = envText
  .split('\n')
  .find((line) => line.startsWith('MIDNIGHT_PREVIEW_SEED='))
  ?.slice('MIDNIGHT_PREVIEW_SEED='.length)
  ?.trim();
if (!seed) {
  throw new Error('MIDNIGHT_PREVIEW_SEED missing in e2e/.env.preview');
}

setNetworkId(PREVIEW_CONFIG.networkId);
const logger = pino({ level: process.env['LOG_LEVEL'] ?? 'info', transport: { target: 'pino-pretty' } });
const envConfig = {
  walletNetworkId: PREVIEW_CONFIG.networkId,
  networkId: PREVIEW_CONFIG.networkId,
  indexer: PREVIEW_CONFIG.indexer,
  indexerWS: PREVIEW_CONFIG.indexerWS,
  node: PREVIEW_CONFIG.node,
  nodeWS: PREVIEW_CONFIG.nodeWS,
  faucet: PREVIEW_CONFIG.faucet,
  proofServer: PREVIEW_CONFIG.proofServer,
};

const wallet = await MidnightWalletProvider.build(logger, envConfig, { kind: 'seed', value: seed });
await wallet.start();
try {
  await syncWallet(logger, wallet.wallet, Number(process.env['MIDNIGHT_SYNC_TIMEOUT_MS'] ?? 10 * 60_000));
  const night = await waitForFunds(wallet.wallet, envConfig, false, wallet.unshieldedKeystore);
  const state = await firstValueFrom(wallet.wallet.state());
  const dustCoins = state.dust.availableCoins.length;
  const dustBal = state.dust.balance(new Date()).toString();
  const unshielded = wallet.unshieldedKeystore.getBech32Address().asString();
  console.log(
    JSON.stringify(
      {
        unshieldedAddress: unshielded,
        nightBalance: night.toString(),
        dustCoins,
        dustBalance: dustBal,
      },
      null,
      2,
    ),
  );
  if (night === 0n) {
    console.error(`No tNIGHT yet. Fund ${unshielded} at ${PREVIEW_CONFIG.faucet}`);
    process.exitCode = 2;
  }
} finally {
  await wallet.stop().catch(() => undefined);
}
