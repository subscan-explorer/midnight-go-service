import { publicArgument } from './public-arguments.mjs';

// Compose the compiler's field order using official Compact descriptors. Hashing
// and leaf serialization stay in the native runtime, including composite leaves.
export function merkleLeafDescriptor(type, runtime, depth = 0, budget = { remaining: 4096 }) {
  if (depth > 20 || --budget.remaining < 0) throw new Error('public_tree_unavailable');
  const child = value => merkleLeafDescriptor(value, runtime, depth + 1, budget);
  const size = value => {
    if (!Number.isSafeInteger(value) || value < 0 || value > 2048) throw new Error('public_tree_unavailable');
    return value;
  };
  const integer = (maximum, Constructor) => {
    const max = BigInt(maximum);
    if (max < 0n || max >= 1n << 256n) throw new Error('public_tree_unavailable');
    return new Constructor(maximum, Math.max(1, Math.ceil(max.toString(2).length / 8)));
  };
  switch (type?.['type-name']) {
    case 'Field': return runtime.CompactTypeField;
    case 'Boolean': return runtime.CompactTypeBoolean;
    case 'Bytes': return new runtime.CompactTypeBytes(size(type.length));
    case 'Uint': return integer(BigInt(type.maxval), runtime.CompactTypeUnsignedInteger);
    case 'Enum': return integer(size(type.elements.length) - 1, runtime.CompactTypeEnum);
    case 'Vector': {
      const length = size(type.length);
      const element = child(type.type ?? type['element-type']);
      budget.remaining -= length * Math.max(1, element.alignment().length);
      if (budget.remaining < 0) throw new Error('public_tree_unavailable');
      return new runtime.CompactTypeVector(length, element);
    }
    case 'Struct':
    case 'Tuple': {
      const fields = type['type-name'] === 'Struct' ? (type.elements ?? type.fields).map(f => [f.name, child(f.type)])
        : type.types.map((t, i) => [i, child(t)]);
      return {
        alignment: () => fields.flatMap(([, descriptor]) => descriptor.alignment()),
        toValue: value => fields.flatMap(([key, descriptor]) => descriptor.toValue(value[key])),
      };
    }
    default: throw new Error('public_tree_unavailable');
  }
}

export function merkleRoots(data, info, runtime) {
  return Object.entries(data ?? {}).filter(([, value]) => value && typeof value.root === 'function').map(([field, tree]) => {
    const definition = info?.ledger?.find(item => item.name === field && item.exported && /MerkleTree$/.test(item.storage));
    let supported = false;
    if (definition && Number.isInteger(definition.depth) && definition.depth > 0 && definition.depth <= 32) {
      try { merkleLeafDescriptor(definition.type, runtime); supported = true; }
      catch (error) { if (error.message !== 'public_tree_unavailable') throw error; }
    }
    const available = supported && typeof tree.pathForLeaf === 'function';
    return { field, root: tree.root().field.toString(16), depth: definition?.depth,
      tree_type: definition?.storage, leaf_type: definition?.type, can_check_inclusion: available,
      supports_historical_roots: definition?.storage === 'HistoricMerkleTree',
      unavailable_reason: available ? null : 'The verified leaf type or public path is not supported.' };
  });
}

// Generated pathForLeaf embeds its supplied leaf, but the siblings depend only
// on the index. A neutral typed value retrieves those siblings without asking
// the caller to disclose the preimage. Never return that placeholder as proof.
function neutralLeaf(type) {
  switch (type['type-name']) {
    case 'Field': case 'Uint': return 0n;
    case 'Boolean': return false;
    case 'Enum': return 0;
    case 'Bytes': return new Uint8Array(type.length);
    case 'Vector': return Array.from({ length: type.length }, () => neutralLeaf(type.type ?? type['element-type']));
    case 'Tuple': return type.types.map(neutralLeaf);
    case 'Struct': return Object.fromEntries((type.elements ?? type.fields).map(f => [f.name, neutralLeaf(f.type)]));
    default: throw new Error('public_tree_unavailable');
  }
}

function rootField(value, runtime) {
  if (typeof value !== 'string' || !/^(?:0x)?[0-9a-f]{1,64}$/i.test(value)) throw new Error('invalid_merkle_proof');
  const field = BigInt('0x' + value.replace(/^0x/i, ''));
  if (field > runtime.MAX_FIELD) throw new Error('invalid_merkle_proof');
  return field;
}

// Proof paths contain public hashes only, with decimal Field values matching
// Compact's JSON representation. Reject coercion, excessive depth and bad indices.
export function verifyMerklePath(hash, path, index, root, depth, runtime) {
  if (typeof hash !== 'string' || !/^(?:0x)?[0-9a-f]{64}$/i.test(hash)
      || !Array.isArray(path) || path.length !== depth) throw new Error('invalid_merkle_proof');
  let digest = runtime.degradeToTransient(Uint8Array.from(Buffer.from(hash.replace(/^0x/i, ''), 'hex')));
  const pair = new runtime.CompactTypeVector(2, runtime.CompactTypeField);
  let correctIndex = true;
  for (const [level, entry] of path.entries()) {
    const field = entry?.sibling?.field;
    if (typeof entry?.goes_left !== 'boolean' || !(typeof field === 'bigint'
        || typeof field === 'string' && /^(0|[1-9][0-9]{0,77})$/.test(field))) throw new Error('invalid_merkle_proof');
    const sibling = BigInt(field);
    if (sibling < 0n || sibling > runtime.MAX_FIELD) throw new Error('invalid_merkle_proof');
    correctIndex &&= entry.goes_left === ((index >> BigInt(level) & 1n) === 0n);
    digest = runtime.transientHash(pair, entry.goes_left ? [digest, sibling] : [sibling, digest]);
  }
  return correctIndex && digest === rootField(root, runtime);
}

export function checkMerkleInclusion(request, data, info, runtime) {
  // Do not rehash unrelated public trees when checking one indexed leaf.
  const selected = Object.hasOwn(data ?? {}, request.field) ? { [request.field]: data[request.field] } : {};
  const root = merkleRoots(selected, info, runtime)[0];
  if (!root?.can_check_inclusion) throw new Error('public_tree_unavailable');
  let hash, index;
  try {
    if (!/^(0|[1-9][0-9]*)$/.test(request.leaf_index)) throw new Error('index');
    index = BigInt(request.leaf_index);
    if (index >= 1n << BigInt(root.depth)) throw new Error('index');
    if (request.leaf_hash !== undefined && request.leaf !== undefined) throw new Error('ambiguous leaf');
    if (request.leaf_hash !== undefined) {
      if (!/^(?:0x)?[0-9a-f]{64}$/i.test(request.leaf_hash)) throw new Error('hash');
      hash = request.leaf_hash.replace(/^0x/i, '').toLowerCase();
    } else {
      const leaf = publicArgument(root.leaf_type, request.leaf);
      const descriptor = merkleLeafDescriptor(root.leaf_type, runtime);
      hash = Buffer.from(new runtime.CompactTypeBytes(32).fromValue(
        runtime.leafHash({ value: descriptor.toValue(leaf), alignment: descriptor.alignment() }).value)).toString('hex');
    }
  } catch { throw new Error('invalid_merkle_leaf'); }
  const tree = data[request.field];
  const checkedRoot = request.root === undefined ? root.root : rootField(request.root, runtime).toString(16);
  const accepted = checkedRoot === root.root || root.supports_historical_roots && tree.checkRoot({ field: rootField(checkedRoot, runtime) });
  if (!accepted) throw new Error('merkle_root_unavailable');
  let path;
  if (request.proof !== undefined) {
    path = request.proof?.path;
  } else {
    if (checkedRoot !== root.root) throw new Error('historical_proof_required');
    // O(depth) indexed lookup; never scan all leaves to find a preimage.
    try { path = tree.pathForLeaf(index, neutralLeaf(root.leaf_type))?.path; }
    catch { throw new Error('public_tree_unavailable'); }
  }
  const included = verifyMerklePath(hash, path, index, checkedRoot, root.depth, runtime);
  // The generic state serializer emits small BigInts as JSON numbers. Proofs
  // require decimal strings at every size so browser verification and imports
  // use the same lossless representation. Return only public sibling fields.
  const publicPath = included ? { path: path.map(entry => ({
    sibling: { field: BigInt(entry.sibling.field).toString() }, goes_left: entry.goes_left,
  })) } : null;
  return { ...root, current_root: root.root, root: checkedRoot, leaf_index: request.leaf_index,
    leaf_hash: hash, included, path: publicPath };
}
