import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
import { readFileSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.homeDir, 'actual original protocol property corpus')
if (process.env.CC_HAHA_WHATSAPP_LEAF !== '1') throw new Error('Unowned protocol property fixture')
const require = createRequire(new URL('../../../../adapters/package.json', import.meta.url))
const packageRoot = join(dirname(require.resolve('@whiskeysockets/baileys')), '..')
const protoPath = join(packageRoot, 'WAProto/WAProto.proto')
const jsPath = join(packageRoot, 'WAProto/index.js')
const { proto } = await import(jsPath)
const protobuf = require('protobufjs')
const schema = protobuf.parse(readFileSync(protoPath, 'utf8'), { keepCase: true }).root
schema.resolveAll()
const sha256 = (path: string) => createHash('sha256').update(readFileSync(path)).digest('hex')
const authority = { version: require(join(packageRoot, 'package.json')).version, protoPath, protoSHA256: sha256(protoPath), jsPath, jsSHA256: sha256(jsPath), packageSHA256: sha256(join(packageRoot, 'package.json')) }
const options = { bytes: String, longs: String, enums: Number, json: true }
const types = new Map<string, any>()
function collect(scope: any, prefix: string) {
  for (const [key, value] of Object.entries(scope)) {
    if (!value || !['object', 'function'].includes(typeof value)) continue
    const name = prefix ? `${prefix}.${key}` : key
    if (typeof (value as any).encode === 'function' && typeof (value as any).decode === 'function' && typeof (value as any).toObject === 'function') types.set(name, value)
    collect(value, name)
  }
}
collect(proto, '')
if (input.wires) {
  const rows = input.wires.map((row: any) => {
    const type = types.get(row.type)
    if (!type) throw new Error(`Original protocol type unavailable: ${row.type}`)
    return { type: row.type, name: row.name, known: type.toObject(type.decode(Buffer.from(row.wire, 'base64')), { ...options, oneofs: true }) }
  })
  await Bun.write(Bun.stdout, JSON.stringify(rows) + '\n')
  process.exit(0)
}
const records = []
function normalized(code: string) {
  return code.replace(/\bm\b/g, 'message').replace(/\bw\b/g, 'writer').replace(/\bo\b/g, 'options').replace(/\bd\b/g, 'object').replace(/\bj\b/g, 'i')
}
function metadata(type: any, field: string) {
  const encode = normalized(type.encode.toString())
  const object = normalized(type.toObject.toString())
  const escaped = field.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const scalar = new RegExp(`writer\\.(?:u?int32\\(\\d+\\)\\.)?(bool|string|bytes|float|double|[su]?int32|[su]?int64|[s]?fixed32|[s]?fixed64)\\(message\\.${escaped}(?:\\[i\\])?\\)`).exec(encode)?.[1]
  const messageType = new RegExp(`\\$root\\.proto\\.([A-Za-z0-9_$.]+)\\.encode\\(message\\.${escaped}(?:\\[i\\])?`, 'm').exec(encode)?.[1]
  const selector = new RegExp(`if \\(options\\.oneofs\\)\\s*object\\.([A-Za-z0-9_$]+) = ["']${escaped}["']`).exec(object)?.[1]
  return { scalar, messageType, selector }
}
function seed(type: any, depth = 0, seen = new Set<any>()): any {
  const fields = Object.keys(type.prototype).filter(name => name !== 'toJSON')
  const input: any = {}
  const selectors = new Set<string>()
  for (const field of fields) {
    const { scalar, messageType, selector } = metadata(type, field)
    if (selector && selectors.has(selector)) continue
    if (selector) selectors.add(selector)
    const prototype = type.prototype[field]
    if (Array.isArray(prototype)) input[field] = []
    else if (scalar === 'bytes') input[field] = Buffer.alloc(0)
    else if (scalar === 'string') input[field] = ''
    else if (scalar === 'bool') input[field] = false
    else if (scalar?.endsWith('64') && scalar !== 'double') input[field] = '0'
    else if (scalar) input[field] = 0
    else if (messageType && depth < 2 && !seen.has(types.get(messageType))) input[field] = seed(types.get(messageType), depth + 1, new Set([...seen, type]))
  }
  // A positive transport case carries a real message key. The original
  // proto3 decoder accepts its absence; the native WebMessageInfo proto2
  // schema marks it required. That strictness difference is recorded in the
  // inventory, while this positive corpus uses Source-produced valid keys.
  if (type === proto.WebMessageInfo && !input.key) input.key = seed(proto.MessageKey, depth + 1, seen)
  return input
}
for (const [name, type] of types) {
  const defaults = type.toObject(type.create(), { ...options, defaults: true, arrays: true, objects: true })
  const prototypes: any = {}
  const fields = Object.keys(type.prototype).filter(name => name !== 'toJSON')
  const baseline = seed(type)
  const descriptor = schema.lookupType(`proto.${name}`)
  const fieldSchemas: any = {}
  for (const field of fields) {
    const { scalar, messageType } = metadata(type, field)
    const fd: any = descriptor.fields[field] ?? Object.values(descriptor.fields).find((value: any) => value.name.toLowerCase().replaceAll('_', '') === field.toLowerCase().replaceAll('_', ''))
    if (!fd) throw new Error(`Actual generated constructor field lacks original schema ${name}.${field}`)
    const enumeration = fd.resolvedType instanceof protobuf.Enum ? Object.entries(fd.resolvedType.values).filter(([key]) => !/^-?\d+$/.test(key)) : null
    fieldSchemas[field] = { tag: fd.id, type: fd.type, repeated: fd.repeated, map: fd.map, keyType: fd.keyType, optional: fd.optional, oneof: fd.partOf?.name ?? null, enum: enumeration, default: enumeration ? enumeration[0][1] : null }
    prototypes[field] = type.prototype[field]
    if (!(field in defaults)) {
      if (scalar) {
        const explicit = type.fromObject({ [field]: scalar === 'bytes' ? Buffer.alloc(0) : scalar === 'string' ? '' : scalar === 'bool' ? false : scalar.endsWith('64') && scalar !== 'double' ? '0' : enumeration ? enumeration[0][1] : 0 })
        defaults[field] = type.toObject(type.decode(type.encode(explicit).finish()), options)[field]
      } else if (messageType || type.prototype[field] === null) defaults[field] = null
    }
  }
  const zero = type.create()
  let defaultWire = null, defaultError = null
  try { const wire = type.encode(zero).finish(); type.decode(wire); defaultWire = Buffer.from(wire).toString('base64') }
  catch (error) { defaultError = error instanceof Error ? error.message : String(error) }
  const cases = []
  const selectorCounts = new Map<string, number>()
  for (const field of fields) {
    const selector = metadata(type, field).selector
    if (selector) selectorCounts.set(selector, (selectorCounts.get(selector) ?? 0) + 1)
  }
  for (const field of fields) {
    const { scalar, messageType, selector } = metadata(type, field)
    const repeated = Array.isArray(defaults[field])
    const nullable = defaults[field] === null
    let value: any
    if (scalar === 'bytes') value = Buffer.from([0, 1, 128, 255])
    else if (scalar === 'string') value = 'original π 🧪'
    else if (scalar === 'bool') value = true
    else if (scalar?.endsWith('64') && scalar !== 'double') value = scalar.startsWith('u') || scalar === 'fixed64' ? '18446744073709551615' : '9223372036854775807'
    else if (scalar === 'float' || scalar === 'double') value = 1.5
    else if (scalar) value = 1
    else if (messageType) value = seed(types.get(messageType), 1)
    else if (nullable) value = {}
    else if (repeated) value = 1
    else if (typeof defaults[field] === 'object') value = { original: 1 }
    else value = typeof defaults[field] === 'string' ? 'original' : typeof defaults[field] === 'boolean' ? true : 1
    if (repeated) value = [value]
    const enumValues = fieldSchemas[field].enum?.map((row: any[]) => row[1]) ?? []
    const candidates = enumValues.length ? [...new Set([...enumValues, 2147483647])].map((value: number) => repeated ? [value] : value) : [value]
    for (const candidate of candidates) {
    try {
      // The actual fromObject conversion and physical codec, not the Go
      // descriptor, decide whether this external field input is accepted.
      const supplied = { ...baseline, [field]: candidate }
      if (selector) for (const other of fields) if (other !== field && metadata(type, other).selector === selector) delete supplied[other]
      const message = type.fromObject(supplied)
      const wire = type.encode(message).finish()
      const decoded = type.decode(wire)
      const known = type.toObject(decoded, { ...options, oneofs: true })
      cases.push({ field, scalar: scalar ?? null, messageType: messageType ?? null, selector: selector ?? null, synthetic: selector ? selectorCounts.get(selector) === 1 : false, wire: Buffer.from(wire).toString('base64'), expected: known[field] ?? defaults[field], present: Object.prototype.hasOwnProperty.call(decoded, field), known })
    } catch (error) {
      cases.push({ field, scalar: scalar ?? null, messageType: messageType ?? null, selector: selector ?? null, sourceError: error instanceof Error ? error.message : String(error) })
    }
    }
  }
  records.push({ type: name, defaults, prototypes, fieldSchemas, authority, defaultWire, defaultError, cases })
}
process.stdout.write(JSON.stringify(records) + '\n')
