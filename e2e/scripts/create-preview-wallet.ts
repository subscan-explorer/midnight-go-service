import { randomBytes } from 'node:crypto';
import { writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { setNetworkId } from '@midnight-ntwrk/midnight-js-network-id';
import pino from 'pino';

import { PREVIEW_CONFIG } from '../src/config.js';
import { MidnightWalletProvider } from '../src/wallet.js';

const e2eDir = join(dirname(fileURLToPath(import.meta.url)), '..');
const seed = randomBytes(32).toString('hex');

setNetworkId(PREVIEW_CONFIG.networkId);

const logger = pino({ level: 'error' });
const wallet = await MidnightWalletProvider.build(
  logger,
  {
    walletNetworkId: PREVIEW_CONFIG.networkId,
    networkId: PREVIEW_CONFIG.networkId,
    indexer: PREVIEW_CONFIG.indexer,
    indexerWS: PREVIEW_CONFIG.indexerWS,
    node: PREVIEW_CONFIG.node,
    nodeWS: PREVIEW_CONFIG.nodeWS,
    faucet: PREVIEW_CONFIG.faucet,
    proofServer: PREVIEW_CONFIG.proofServer,
  },
  { kind: 'seed', value: seed },
);

const unshielded = wallet.unshieldedKeystore.getBech32Address().asString();
writeFileSync(
  join(e2eDir, '.env.preview'),
  `# gitignored. Fund this address on Preview then re-run bash e2e/run.sh\nMIDNIGHT_PREVIEW_SEED=${seed}\n`,
  { mode: 0o600 },
);
await wallet.stop().catch(() => undefined);

console.log(JSON.stringify({ faucet: PREVIEW_CONFIG.faucet, unshieldedAddress: unshielded }, null, 2));
