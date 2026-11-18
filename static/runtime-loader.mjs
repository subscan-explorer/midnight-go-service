import { readFile } from 'node:fs/promises';

const runtimes = new Map([
  ['0.14.0', '@midnight-ntwrk/compact-runtime-0.14'],
  ['0.15.0', '@midnight-ntwrk/compact-runtime-0.15'],
  ['0.16.0', '@midnight-ntwrk/compact-runtime'],
  ['0.18.0-rc.1', '@midnight-ntwrk/compact-runtime-0.18'],
  ['0.20.0', '@midnight-ntwrk/compact-runtime-0.20'],
  ['0.21.0', '@midnight-ntwrk/compact-runtime-0.21'],
]);

function runtimePackage(source) {
  const version = source.match(/checkRuntimeVersion\(\s*['"]([^'"]+)['"]\s*\)/)?.[1];
  if (!version) return null;
  const runtime = runtimes.get(version);
  if (!runtime) throw new Error(`Unsupported compiled Compact runtime: ${version}`);
  return runtime;
}

export async function contractRuntime(moduleURL) {
  const runtime = runtimePackage(await readFile(moduleURL, 'utf8'));
  if (!runtime) throw new Error('Compiled contract does not declare a Compact runtime');
  return import(runtime);
}

// Generated contracts declare their runtime version. Route only that import;
// SDK dependencies keep the Ledger 8 runtime selected by their own package.
export async function resolve(specifier, context, nextResolve) {
  if (specifier !== '@midnight-ntwrk/compact-runtime' || !context.parentURL?.startsWith('file:')) {
    return nextResolve(specifier, context);
  }
  if (!/\.(?:js|mjs|ts)$/.test(new URL(context.parentURL).pathname)) return nextResolve(specifier, context);
  const source = await readFile(new URL(context.parentURL), 'utf8');
  const runtime = runtimePackage(source);
  // Artifacts may live on a mounted PVC or outside the application's directory.
  // Resolve pinned runtimes from this loader, not from the artifact's ancestors.
  return nextResolve(runtime ?? specifier, runtime ? { ...context, parentURL: import.meta.url } : context);
}
