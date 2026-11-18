import { readFile } from 'node:fs/promises';
import { join } from 'node:path';

// Keep compiler integer bounds lossless, including Uint<64/128>, in JSON output.
export async function readCompilerInfo(directory) {
  return JSON.parse(await readFile(join(directory, 'compiler/contract-info.json'), 'utf8'), (key, value, context) => {
    if (key !== 'maxval' || typeof value !== 'number') return value;
    if (context?.source) return context.source;
    if (!Number.isSafeInteger(value)) throw new Error('unsupported_runtime');
    return String(value);
  });
}

// Compiler metadata is the authority for conversion. JSON numbers must never
// silently round a Compact Field or Uint before a wallet signs the result.
export function publicArgument(type, value, depth = 0) {
  if (depth > 20) throw new Error('Argument nesting exceeds 20 levels');
  const name = type?.['type-name'];
  const child = (t, v) => publicArgument(t, v, depth + 1);
  switch (name) {
    case 'Field':
    case 'Uint': {
      if (!(typeof value === 'string' && /^(0|[1-9][0-9]*)$/.test(value))
          && !(typeof value === 'number' && Number.isSafeInteger(value) && value >= 0)) {
        throw new Error('Integers must be unsigned decimal strings or safe JSON integers');
      }
      const result = BigInt(value);
      const bits = type.bits ?? type.width;
      if (bits !== undefined && (!Number.isInteger(bits) || bits < 0 || bits > 256)) throw new Error('Invalid integer width');
      if (name === 'Uint' && Number.isInteger(bits) && result >= 1n << BigInt(bits)) throw new Error('Uint out of range');
      if (name === 'Uint' && type.maxval !== undefined && result > BigInt(type.maxval)) throw new Error('Uint out of range');
      return result;
    }
    case 'Boolean':
      if (typeof value !== 'boolean') throw new Error('Expected a boolean');
      return value;
    case 'Bytes':
      if (typeof value !== 'string' || !/^(?:0x)?(?:[a-f0-9]{2})*$/i.test(value)) throw new Error('Expected hex bytes');
      const bytes = Uint8Array.from(Buffer.from(value.replace(/^0x/, ''), 'hex'));
      if (bytes.length !== type.length) throw new Error('Incorrect Bytes length');
      return bytes;
    case 'Opaque':
      if (type.tsType === 'string' && typeof value === 'string') return value;
      if (type.tsType === 'Uint8Array' && typeof value === 'string') return child({ 'type-name': 'Bytes', length: value.replace(/^0x/, '').length / 2 }, value);
      throw new Error('Unsupported Opaque representation');
    case 'Tuple':
      if (!Array.isArray(value) || value.length !== type.types.length) throw new Error('Incorrect tuple length');
      return type.types.map((t, i) => child(t, value[i]));
    case 'Vector':
      if (!Array.isArray(value) || value.length !== type.length) throw new Error('Incorrect vector length');
      return value.map(v => child(type['element-type'] ?? type.type, v));
    case 'Struct': {
      if (!value || Array.isArray(value) || typeof value !== 'object') throw new Error('Expected a struct');
      const fields = type.elements ?? type.fields;
      if (!Array.isArray(fields) || Object.keys(value).length !== fields.length || fields.some(f => !Object.hasOwn(value, f.name))) throw new Error('Incorrect struct fields');
      return Object.fromEntries(fields.map(f => [f.name, child(f.type, value[f.name])]));
    }
    case 'Enum':
      if (!Array.isArray(type.elements) || !Number.isSafeInteger(value) || value < 0 || value >= type.elements.length) throw new Error('Expected an enum ordinal');
      return value;
    default: throw new Error(`Unsupported public argument type: ${name ?? 'unknown'}`);
  }
}

export function circuitArguments(info, circuit, values) {
  const definition = info.circuits?.find(item => item.name === circuit);
  if (!definition || !definition.proof || definition.pure) throw new Error('Select a public provable circuit');
  if (!Array.isArray(values) || values.length !== definition.arguments.length) throw new Error('Incorrect argument count');
  return definition.arguments.map((arg, i) => publicArgument(arg.type, values[i]));
}
