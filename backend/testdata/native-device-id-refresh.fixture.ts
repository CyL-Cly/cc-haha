import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import fs from 'node:fs'
import { syncBuiltinESMExports } from 'node:module'
import { spyOn } from 'bun:test'
import { join } from 'node:path'
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.homeDir, 'actual Source device ID refresh')
if (process.env.CC_HAHA_DEVICE_ID_REFRESH_FIXTURE !== '1') throw new Error('Unowned identity fixture')
await import('../../preload')
let originalCallback: ((value: fs.Stats) => void) | undefined
let observedInterval = 0
const nativeFS = require('fs')
const realWatch = nativeFS.watchFile
const observeWatch = (path: string, options: any, callback: (value: fs.Stats) => void) => {
  originalCallback = callback
  observedInterval = options.interval
  return realWatch(path, options, callback)
}
nativeFS.watchFile = observeWatch
spyOn(await import('fs'), 'watchFile').mockImplementation(observeWatch as any)
syncBuiltinESMExports()
const file = join(process.env.CLAUDE_CONFIG_DIR!, '.claude.json')
fs.mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
const initial = input.initial ? JSON.parse(input.initial) : { userID: 'owned-existing-device' }
fs.writeFileSync(file, JSON.stringify({ ...initial, future: { keep: true } }))
const original = await import('../../src/utils/config')
original.enableConfigs()
const actualFile = (await import('../../src/utils/env')).getGlobalClaudeFile()
if (actualFile !== file) throw new Error(`Original selected unexpected file ${actualFile}`)
const first = original.getOrCreateUserID()
const firstGenerated = !initial.userID
if (firstGenerated && !/^[0-9a-f]{64}$/.test(first)) throw new Error('Invalid original generated identity')
fs.writeFileSync(file, JSON.stringify({ projects: { owned: { disabledMcpServers: ['owned'] } }, future: { keep: true } }))
const fresh = new Date(Date.now() + 1000)
fs.utimesSync(file, fresh, fresh)
if (!originalCallback || observedInterval !== 1000) throw new Error('Original watcher primitive not bound')
// Explicit external stat notification invokes the unchanged Source callback;
// its real file read/cache update must complete before the next request.
originalCallback(fs.statSync(file))
const deadline = Date.now() + 5000
while (original.getGlobalConfig().userID !== undefined) {
  if (Date.now() >= deadline) throw new Error('Original real freshness watcher did not publish owned input '+JSON.stringify(original.getGlobalConfig()))
  await new Promise(resolve => setTimeout(resolve, 10))
}
const second = original.getOrCreateUserID()
const current = original.getGlobalConfig()
fs.unwatchFile(file)
process.stdout.write(JSON.stringify({ first: firstGenerated ? '<GENERATED32HEX>' : first, firstGenerated, second, generatedHex32: /^[0-9a-f]{64}$/.test(second), cached: current.userID === second, future: current.future, projects: current.projects }) + '\n')
