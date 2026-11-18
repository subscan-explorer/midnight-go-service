import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { cp, mkdir, mkdtemp, rm } from 'node:fs/promises';
import { createServer } from 'node:http';
import path from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';

const root = fileURLToPath(new URL('../', import.meta.url));

async function listen(server) {
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  return server.address().port;
}

async function within(promise, timeout, description) {
  let timer;
  try {
    return await Promise.race([
      promise,
      new Promise((_, reject) => {
        timer = setTimeout(() => reject(new Error(description)), timeout);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}

test('HTTP client cancellation closes the Node reader Indexer request', { timeout: 30_000 }, async () => {
  let reachedIndexer;
  let disconnected;
  const requested = new Promise((resolve) => { reachedIndexer = resolve; });
  const closed = new Promise((resolve) => { disconnected = resolve; });
  const indexer = createServer((request, response) => {
    request.resume();
    request.once('end', reachedIndexer);
    // A real reader request stays in flight until cancellation kills Node.
    response.once('close', disconnected);
  });
  const reservation = createServer();
  const workDir = await mkdtemp(path.join(root, '.e2e-reader-cancel-'));
  let service;
  const controller = new AbortController();
  let read;
  try {
    const indexerPort = await listen(indexer);
    const servicePort = await listen(reservation);
    await new Promise((resolve) => reservation.close(resolve));
    const staticDir = path.join(workDir, 'static');
    await mkdir(staticDir);
    for (const asset of ['state.mjs', 'read-state.mjs', 'register-runtime.mjs', 'runtime-loader.mjs']) {
      await cp(path.join(root, 'static', asset), path.join(staticDir, asset));
    }
    const endpoint = `http://127.0.0.1:${indexerPort}/graphql?project_id=E2E_PRIVATE_TOKEN`;
    service = spawn(path.join(root, 'e2e', '.midnight-server'), [], {
      cwd: root,
      env: {
        ...process.env,
        NETWORK_ID: 'preview',
        PORT: String(servicePort),
        STATIC_DIR: staticDir,
        TEMP_DIR: path.join(workDir, 'tmp'),
        MIDNIGHT_PREVIEW_INDEXER_RPC: endpoint,
        MIDNIGHT_PREVIEW_INDEXER_WS: endpoint.replace('http:', 'ws:'),
        MIDNIGHT_PREVIEW_RPC_NODE: endpoint,
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let serviceLogs = '';
    service.stdout.on('data', (chunk) => { serviceLogs = (serviceLogs + chunk).slice(-65536); });
    let serviceError = '';
    service.stderr.on('data', (chunk) => { serviceError = (serviceError + chunk).slice(-4096); });
    const exited = once(service, 'exit');
    const outputClosed = once(service, 'close');
    const url = `http://127.0.0.1:${servicePort}/contract/state`;
    let ready = false;
    for (let attempt = 0; attempt < 50; attempt++) {
      assert.equal(service.exitCode, null, serviceError);
      try {
        const probe = await fetch(url, { signal: AbortSignal.timeout(500) });
        await probe.text();
        if (probe.status === 400) {
          ready = true;
          break;
        }
      } catch {
        // The child may not have bound its port yet.
      }
      await delay(50);
    }
    assert.ok(ready, `service did not start: ${serviceError}`);
    // Attach a rejection handler before aborting, avoiding an unhandled promise.
    read = fetch(`${url}?contract_addr=${'ab'.repeat(32)}`, { signal: controller.signal, headers: { 'X-Request-ID': 'canceled-reader-check' } })
      .then(() => assert.fail('stalled Indexer unexpectedly returned a response'), (error) => error);
    await within(requested, 5000, 'HTTP request never reached the Indexer');
    controller.abort();
    assert.equal((await read).name, 'AbortError');
    await within(closed, 3000, 'canceled Node reader kept its Indexer connection open');
    const probe = await fetch(url, { signal: AbortSignal.timeout(1000) });
    await probe.text();
    assert.equal(probe.status, 400, 'service should remain responsive after cancellation');
    assert.match(probe.headers.get('X-Request-ID'), /^[a-f0-9]{32}$/);
    service.kill('SIGTERM');
    // Shutdown drains requests for up to 15 seconds. Fresh idle connections
    // from the HTTP client's pool can take about 5 seconds to become quiescent.
    const [exitCode] = await within(exited, 17_000, 'service did not stop');
    assert.equal(exitCode, 0, 'service should finish graceful shutdown');
    await within(outputClosed, 1000, 'service output did not close');
    const records = serviceLogs.trim().split('\n').map((line) => JSON.parse(line));
    const canceled = records.filter((record) => record.request_id === 'canceled-reader-check');
    assert.equal(canceled.filter((record) => record.event === 'request_started').length, 1);
    const completed = canceled.filter((record) => record.event === 'request_completed');
    assert.equal(completed.length, 1);
    assert.equal(completed[0].outcome, 'canceled');
    assert.equal(completed[0].contract_addr, 'ab'.repeat(32));
    assert.equal(completed[0].network, 'preview');
    assert.ok(canceled.some((record) => record.event === 'operation_completed' && record.error_code === 'canceled' && record.phase === 'running'));
    assert.ok(records.some((record) => record.event === 'server_started'));
    assert.ok(records.some((record) => record.event === 'server_stopping'));
    assert.ok(records.some((record) => record.event === 'server_stopped' && record.drained));
    assert.doesNotMatch(serviceLogs, /E2E_PRIVATE_TOKEN|project_id|INDEXER_RPC/);
  } finally {
    controller.abort();
    if (read) await read;
    if (service && service.exitCode === null && service.signalCode === null) {
      const exited = once(service, 'exit');
      service.kill('SIGKILL');
      await exited;
    }
    if (reservation.listening) await new Promise((resolve) => reservation.close(resolve));
    indexer.closeAllConnections();
    await new Promise((resolve) => indexer.close(resolve));
    await rm(workDir, { recursive: true, force: true });
  }
});
