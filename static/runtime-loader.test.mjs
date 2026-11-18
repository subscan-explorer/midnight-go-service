import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { resolve } from './runtime-loader.mjs';

test('generated contracts select their declared runtime and reject unknown versions', async t => {
  const dir = await mkdtemp(join(tmpdir(), 'midnight-runtime-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  for (const [version, target] of [['0.14.0', '-0.14'], ['0.15.0', '-0.15'], ['0.16.0', ''], ['0.18.0-rc.1', '-0.18'], ['0.20.0', '-0.20'], ['0.21.0', '-0.21']]) {
    const file = join(dir, `${version}.js`);
    await writeFile(file, `runtime.checkRuntimeVersion('${version}');`);
    const actual = await resolve('@midnight-ntwrk/compact-runtime', { parentURL: pathToFileURL(file).href }, async specifier => specifier);
    assert.equal(actual, `@midnight-ntwrk/compact-runtime${target}`);
  }
  const file = join(dir, 'future.js');
  await writeFile(file, `runtime.checkRuntimeVersion('0.99.0');`);
  await assert.rejects(resolve('@midnight-ntwrk/compact-runtime', { parentURL: pathToFileURL(file).href }, async s => s), /Unsupported compiled/);
});
