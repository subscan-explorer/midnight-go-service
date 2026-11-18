import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { setNetworkId } from '@midnight-ntwrk/midnight-js-network-id';
import { firstValueFrom, throwError } from 'rxjs';
import { filter, take, tap, timeout } from 'rxjs/operators';
import pino from 'pino';

import { PREVIEW_CONFIG } from '../src/config.js';
import { MidnightWalletProvider, syncWallet } from '../src/wallet.js';

const e2eDir = join(dirname(fileURLToPath(import.meta.url)), '..');
const seed = readFileSync(join(e2eDir, '.env.preview'), 'utf8')
  .split('\n')
  .find((line) => line.startsWith('MIDNIGHT_PREVIEW_SEED='))
  ?.slice('MIDNIGHT_PREVIEW_SEED='.length)
  ?.trim();
if (!seed) throw new Error('MIDNIGHT_PREVIEW_SEED missing');

setNetworkId(PREVIEW_CONFIG.networkId);
const logger = pino({ level: 'info', transport: { target: 'pino-pretty' } });
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

const timeoutMs = Number(process.env['WAIT_FOR_DUST_TIMEOUT_MS'] ?? 8 * 60_000);
const minCoins = Number(process.env['WAIT_FOR_DUST_MIN_COINS'] ?? 1);

const wallet = await MidnightWalletProvider.build(logger, envConfig, { kind: 'seed', value: seed });
await wallet.start();
try {
  await syncWallet(logger, wallet.wallet, timeoutMs);
  await firstValueFrom(
    wallet.wallet.state().pipe(
      tap((s) =>
        logger.info(
          `dust coins=${s.dust.availableCoins.length} balance=${s.dust.balance(new Date())} nightUtxos=${s.unshielded.availableCoins.length}`,
        ),
      ),
      filter((s) => s.dust.availableCoins.length >= minCoins),
      take(1),
      timeout({
        each: timeoutMs,
        with: () => throwError(() => new Error(`No spendable DUST coin within ${timeoutMs}ms`)),
      }),
    ),
  );
  logger.info('DUST ready');
} finally {
  await wallet.stop().catch(() => undefined);
}
