import { createRequire } from 'node:module'
import { createHash } from 'node:crypto'
import { readdirSync, readFileSync } from 'node:fs'
import { isAbsolute, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'

const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.homeDir, 'independent native public protocol ABI')
if (process.env.CC_HAHA_WHATSAPP_LEAF !== '1') throw new Error('Unowned native protocol ABI fixture')
const require = createRequire(new URL('../../../../adapters/package.json', import.meta.url))
const protobuf = require('protobufjs')
const directory = fileURLToPath(new URL('../../../third_party/whatsmeow-node/proto/', import.meta.url))
const files = readdirSync(directory, { withFileTypes: true }).filter(row => row.isDirectory()).flatMap(row => readdirSync(join(directory, row.name)).filter(file => file.endsWith('.proto')).map(file => `${row.name}/${file}`)).sort()
const root = new protobuf.Root()
root.resolvePath = (_origin: string, target: string) => isAbsolute(target) ? target : join(directory, target)
root.loadSync(files.map(file => join(directory, file)), { keepCase: true })
root.resolveAll()
const options = { bytes: String, longs: String, enums: Number, json: true, oneofs: true }
const authority = {
  scope: 'independent-pinned-native-proto-not-original-equivalence',
  protobufVersion: require('protobufjs/package.json').version,
  schemas: files.map(file => ({ file, sha256: createHash('sha256').update(readFileSync(join(directory, file))).digest('hex') })),
}
if (input.wires) {
  const rows = input.wires.map((row: any) => ({ type: row.type, name: row.name, known: root.lookupType(row.type).toObject(root.lookupType(row.type).decode(Buffer.from(row.wire, 'base64')), options) }))
  await Bun.write(Bun.stdout, JSON.stringify({ authority, rows }) + '\n')
  process.exit(0)
}
const scalar = (field: any, depth: number): any => {
  if (field.resolvedType instanceof protobuf.Type) return seed(field.resolvedType, depth + 1)
  if (field.resolvedType instanceof protobuf.Enum) return Object.values(field.resolvedType.values)[0]
  if (field.type === 'bytes') return Buffer.from([0, 1, 128, 255])
  if (field.type === 'string') return 'independent native π 🧪'
  if (field.type === 'bool') return true
  if (field.type === 'float' || field.type === 'double') return 1.5
  if (field.type.endsWith('64')) return field.type.startsWith('u') || field.type === 'fixed64' ? '18446744073709551615' : '9223372036854775807'
  return 7
}
const value = (field: any, depth: number): any => {
  const item = scalar(field, depth)
  if (field.map) return { [field.keyType === 'string' ? 'independent-key' : field.keyType === 'bool' ? 'true' : '7']: item }
  return field.repeated ? [item] : item
}
function seed(type: any, depth = 0): any {
  const output: any = {}
  const selected = new Set<string>()
  for (const field of type.fieldsArray) {
    if (field.partOf && selected.has(field.partOf.name)) continue
    if (depth >= 3 && !field.required && field.resolvedType instanceof protobuf.Type) continue
    if (depth > 8) throw new Error(`Required message recursion cannot produce a finite wire: ${type.fullName}`)
    if (field.partOf) selected.add(field.partOf.name)
    output[field.name] = value(field, depth)
  }
  return output
}
const records = input.types.map((name: string) => {
  const type = root.lookupType(name)
  const baseline = seed(type)
  const supplied = [{ name: 'populated', fields: baseline }]
  for (const oneof of type.oneofsArray) {
    for (const fieldName of oneof.oneof) {
      const fields = { ...baseline }
      for (const sibling of oneof.oneof) delete fields[sibling]
      fields[fieldName] = value(type.fields[fieldName], 0)
      supplied.push({ name: `oneof:${oneof.name}:${fieldName}`, fields })
    }
  }
  const fields = Object.fromEntries(type.fieldsArray.map((field: any) => [field.name, { tag: field.id, type: field.type, repeated: field.repeated, map: field.map, oneof: field.partOf?.name ?? null }]))
  const cases = supplied.map(row => {
    const message = type.fromObject(row.fields)
    const error = type.verify(message)
    if (error) throw new Error(`${name}/${row.name}: independent schema rejected supplied fields: ${error}`)
    const wire = type.encode(message).finish()
    return { name: row.name, wire: Buffer.from(wire).toString('base64'), expected: type.toObject(type.decode(wire), options) }
  })
  const defaults = type.toObject(type.create(), { ...options, defaults: true, arrays: true, objects: true })
  for (const field of type.fieldsArray) {
    if (field.name in defaults) continue
    // An inactive oneof is omitted by toObject. Obtain its public getter's
    // default from the independently parsed schema/dynamic constructor.
    defaults[field.name] = field.resolvedType instanceof protobuf.Type ? null : type.toObject(type.fromObject({ [field.name]: field.defaultValue }), options)[field.name]
  }
  return { type: name, fields, defaults, cases }
})
const enums = input.enums.map((name: string) => ({ type: name, symbols: root.lookupEnum(name).values }))
await Bun.write(Bun.stdout, JSON.stringify({ authority, records, enums }) + '\n')
