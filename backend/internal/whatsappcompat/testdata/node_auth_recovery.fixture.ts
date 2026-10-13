import * as fs from 'node:fs'
import * as path from 'node:path'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'WhatsApp actual cold authentication recovery')
if (process.env.CC_HAHA_WHATSAPP_LEAF !== '1') throw new Error('Unowned old authentication recovery fixture')
const require = createRequire(new URL('../../../../adapters/package.json', import.meta.url))
const root = path.join(path.dirname(require.resolve('@whiskeysockets/baileys')), '..')
const { BufferJSON } = await import(path.join(root, 'lib/Utils/generics.js'))
const { initAuthCreds } = await import(path.join(root, 'lib/Utils/auth-utils.js'))
const { useMultiFileAuthState } = await import(path.join(root, 'lib/Utils/use-multi-file-auth-state.js'))
const { adapterMigrationLifecycle } = await import('../../../../adapters/common/migration-lifecycle')
const source = fs.readFileSync(new URL('../../../../adapters/whatsapp/session.ts', import.meta.url), 'utf8')
const begin = source.indexOf('function resolveCredsPath('), end = source.indexOf('function makeBaileysLogger(', begin)
if (begin < 0 || end < 0) throw new Error('Actual authentication helper slice missing')
const queue = new Map<string, Promise<void>>()
const warnings: string[] = []
const helpers = new Function('fs', 'path', 'credsSaveQueues', 'CREDS_FILE', 'CREDS_BACKUP_FILE', 'console', 'adapterMigrationLifecycle',
  new Bun.Transpiler({ loader: 'ts' }).transformSync(source.slice(begin, end)) + ';return { restore:maybeRestoreCredsFromBackup, enqueue:enqueueSaveCreds }')(
  fs, path, queue, 'creds.json', 'creds.json.bak', { warn(...values: unknown[]) { warnings.push(String(values[1])) } }, adapterMigrationLifecycle)

function disk(file: string) {
  try {
    const info = fs.statSync(file)
    if (info.isDirectory()) return { kind: 'directory', mode: info.mode & 0o777 }
    return { kind: 'file', mode: info.mode & 0o777, text: fs.readFileSync(file, 'utf8') }
  } catch (error: any) {
    if (error.code === 'ENOENT') return { kind: 'absent' }
    throw error
  }
}
if (input.consume) {
  const directory = path.resolve(input.consume)
  if (!directory.startsWith(home + path.sep)) throw new Error('Unowned cold authentication consumer')
  const { state } = await useMultiFileAuthState(directory)
  process.stdout.write(JSON.stringify({ creds: state.creds }) + '\n')
  process.exit(0)
}
const rows = []
for (const profile of input.profiles) {
  const directory = path.join(home, profile.name)
  fs.mkdirSync(directory)
  const creds = initAuthCreds()
  Object.assign(creds, { platform: 'android', privateFuture: { version: 37, nested: ['keep', '中文😀'] }, me: { id: '111:1@s.whatsapp.net', lid: '222@lid', name: 'original-name', privateFuture: true } })
  const beforeCreds = JSON.stringify(creds, BufferJSON.replacer)
  const current = path.join(directory, 'creds.json'), backup = current + '.bak'
  for (const [file, kind] of [[current, profile.current], [backup, profile.backup]] as const) {
    if (kind === 'directory') fs.mkdirSync(file)
    else if (kind === 'valid') fs.writeFileSync(file, beforeCreds, { mode: 0o644 })
    else if (kind === 'broken') fs.writeFileSync(file, '{broken private fixture', { mode: 0o644 })
  }
  const before = { current: disk(current), backup: disk(backup) }
  let error = null
  try { helpers.restore(directory) } catch (value: any) { error = value.code }
  let cold = null, saved = null
  if (!error) {
    const { state, saveCreds } = await useMultiFileAuthState(directory)
    cold = JSON.parse(JSON.stringify(state.creds, BufferJSON.replacer))
    if (profile.save) {
      helpers.enqueue(directory, saveCreds)
      await queue.get(path.resolve(directory))
      saved = { current: disk(current), backup: disk(backup), queueCleared: !queue.has(path.resolve(directory)) }
    }
  }
  rows.push({ name: profile.name, before, after: { current: disk(current), backup: disk(backup) }, cold, saved, error })
}
await adapterMigrationLifecycle.quiesce()
process.stdout.write(JSON.stringify({ rows, warnings }) + '\n')
