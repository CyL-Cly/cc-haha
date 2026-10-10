import { spyOn } from 'bun:test'
import fs from 'node:fs'
import { createHash } from 'node:crypto'
import { isAbsolute, join, relative } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'
import type { TLSConfigCase } from './tls-config-fixtures'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { home: string; rows: TLSConfigCase[]; probe?: string }
assertDisposableFixtureHome(input.home, 'TLS config')
if (process.env.CC_HAHA_TLS_CONFIG_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || Bun.version !== '1.3.14' || !Array.isArray(input.rows) || Object.keys(input).some(key => !['home', 'rows', 'probe'].includes(key))) throw new Error('TLS config requires explicit private pinned profile')
for (const row of input.rows) {
  if (typeof row?.name !== 'string' || !Array.isArray(row.operations)) throw new Error('TLS config requires original operation profile')
  for (const op of row.operations) {
    if (!['env', 'write', 'mkdir', 'remove', 'get', 'clear', 'mutate', 'option'].includes(op.op)) throw new Error('TLS config unknown original operation')
    if (op.op === 'env' && (typeof op.value !== 'object' || op.value === null || Array.isArray(op.value) || Object.entries(op.value).some(([key, value]) => !['NODE_OPTIONS', 'NODE_EXTRA_CA_CERTS'].includes(key) || typeof value !== 'string' || key === 'NODE_EXTRA_CA_CERTS' && !['', ' '].includes(value) && !/^file:[a-z]+$/.test(value)))) throw new Error('TLS config requires private original env/path before production imports')
    if (['write', 'mkdir', 'remove'].includes(op.op) && !/^[a-z]+$/.test((op as any).name)) throw new Error('TLS config requires private original filename before production imports')
  }
}
const capabilities = await installClientFixtureCapabilities(input.home)
if (input.probe === 'native') Bun.spawn(['security'])
if (input.probe === 'network') await fetch('http://127.0.0.1:1')
if (input.probe === 'filesystem') fs.readFileSync('/Library/Keychains/System.keychain')
;(globalThis as any).MACRO = { BUILD_TIME: '' }
const { getCACertificates, clearCACertsCache } = await import('../../../src/utils/caCerts')
const { hasNodeOption } = await import('../../../src/utils/envUtils')
const { getFsImplementation } = await import('../../../src/utils/fsOperations')
const files = getFsImplementation(), readOriginal = files.readFileSync.bind(files)
let reads: Array<{ path: string; code: string }> = []
spyOn(files, 'readFileSync').mockImplementation((path, options) => {
  const read = { path: relative(input.home, isAbsolute(path) ? path : join(input.home, path)), code: '' }
  reads.push(read)
  try { return readOriginal(path, options) } catch (error) { read.code = (error as NodeJS.ErrnoException).code ?? ''; throw error }
})
const observations: unknown[] = []
for (const row of input.rows) {
  delete process.env.NODE_OPTIONS
  delete process.env.NODE_EXTRA_CA_CERTS
  clearCACertsCache()
  reads = []
  let current: string[] | undefined
  const identities = new WeakMap<string[], number>()
  let nextID = 0
  const steps: unknown[] = []
  const file = (name: string) => {
    if (!/^[a-z]+$/.test(name)) throw new Error('TLS config private filename required')
    return join(input.home, 'certs', name)
  }
  fs.mkdirSync(join(input.home, 'certs'), { recursive: true })
  for (const op of row.operations) {
    if (op.op === 'write') fs.writeFileSync(file(op.name), Buffer.from(op.hex, 'hex'))
    else if (op.op === 'mkdir') fs.mkdirSync(file(op.name))
    else if (op.op === 'remove') fs.rmSync(file(op.name), { recursive: true })
    else if (op.op === 'env') {
      for (const key of ['NODE_OPTIONS', 'NODE_EXTRA_CA_CERTS']) {
        const value = op.value[key]
        if (value === undefined) delete process.env[key]
        else process.env[key] = key === 'NODE_EXTRA_CA_CERTS' && value.startsWith('file:') ? file(value.slice(5)) : value
      }
    } else if (op.op === 'clear') clearCACertsCache()
    else if (op.op === 'mutate') {
      if (!current) throw new Error('TLS config mutation requires original array')
      if (op.action === 'push') current.push(op.value!)
      else if (op.action === 'pop') current.pop()
      else current[op.index!] = op.value!
    } else if (op.op === 'option') {
      process.env.NODE_OPTIONS = op.options
      steps.push({ option: hasNodeOption(op.flag) })
    } else if (op.op === 'get') {
      // Never read the real OS store. Cache hits containing these new flags
      // remain safe and are an important part of actual Source memo behavior.
      const system = hasNodeOption('--use-system-ca') || hasNodeOption('--use-openssl-ca')
      if (system && !getCACertificates.cache.has(undefined)) throw new Error('TLS config Source system CA profile is unported')
      current = getCACertificates()
      if (current && !identities.has(current)) identities.set(current, ++nextID)
      steps.push({ certificates: current ? { identity: identities.get(current), count: current.length, sha256: createHash('sha256').update(JSON.stringify(current)).digest('hex'), tail: current.slice(145).map(value => JSON.stringify(value)), first: JSON.stringify(current[0]) } : null, reads: structuredClone(reads) })
    }
  }
  observations.push({ name: row.name, steps })
  fs.rmSync(join(input.home, 'certs'), { recursive: true })
}
process.stdout.write(JSON.stringify({ observations, sourceOnly: { capabilities, bun: Bun.version, revision: Bun.revision, profile: 'actual caCerts memo, bundled constant and real private FS; no native trust/socket call' } }))
