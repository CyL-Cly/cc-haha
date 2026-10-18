import fs from 'node:fs'
import path from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual pluginFlagging cache and physical atomic writes')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_PLUGIN_FLAGGED_REGISTRY_SOURCE !== '1') throw new Error('Missing flagged registry Source guard')
let now = input.now
const RealDate = Date
globalThis.Date = class extends RealDate {
  constructor(value?: any) { super(value === undefined ? now : value) }
  static now() { return now }
} as any
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(home)
state.setCwdState(home)
state.setProjectRoot(home)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const registry = await import('../../../src/utils/plugins/pluginFlagging')
const directory = path.join(process.env.CLAUDE_CONFIG_DIR!, 'plugins')
const target = path.join(directory, 'flagged-plugins.json')
fs.mkdirSync(directory, { recursive: true })
fs.writeFileSync(target, input.initial)
let saved = ''
const results = []
for (const step of input.steps) {
  if (step.op === 'load') await registry.loadFlaggedPlugins()
  else if (step.op === 'add') await registry.addFlaggedPlugin(step.id)
  else if (step.op === 'seen') await registry.markFlaggedPluginsSeen(step.ids)
  else if (step.op === 'remove') await registry.removeFlaggedPlugin(step.id)
  else if (step.op === 'advance') now += step.ms
  else if (step.op === 'block') {
    saved = fs.readFileSync(target, 'utf8')
    fs.unlinkSync(target)
    fs.mkdirSync(target)
    fs.writeFileSync(path.join(target, 'physical-blocker'), 'owned failed rename')
  } else if (step.op === 'unblock') {
    fs.rmSync(target, { recursive: true })
    fs.writeFileSync(target, saved)
  } else if (step.op !== 'snapshot') throw new Error('Undeclared Source operation')
  let disk: any = null
  let mode: number | null = null
  if (fs.statSync(target).isFile()) {
    try { disk = JSON.parse(fs.readFileSync(target, 'utf8')) } catch {}
    mode = fs.statSync(target).mode & 0o777
  }
  results.push({ cache: registry.getFlaggedPlugins(), disk, mode, temporaryCount: fs.readdirSync(directory).filter(name => name.endsWith('.tmp')).length })
}
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(results), error => error ? reject(error) : resolve()))
