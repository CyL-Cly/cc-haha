import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { PassThrough } from 'node:stream'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual Buddy mount effect, persistent global and keyboard')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_BUDDY_SOURCE !== '1') throw new Error('Pinned buddy Source required')
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', join(dirname(process.execPath), 'rg')]) if (!fs.existsSync(path)) absentPaths.push(path)
const caps = await installClientFixtureCapabilities(home, { absentPaths })
;(globalThis as any).MACRO = { VERSION: '999.0.0-buddy-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const React = await import('react')
const { render, useInput } = await import('../../../src/ink')
const instances = (await import('../../../src/ink/instances')).default
// The real REPL has an active input subscription. CompanionCard owns DOM
// onKeyDown, which does not itself enable stdin's raw-mode pump.
function ReplInputPump({ children }: any) { useInput(() => {}); return children }
const { call } = await import('../../../src/commands/buddy/buddy')
const declaration = (await import('../../../src/commands/buddy/index')).default
const results = []
for (const row of input.rows) {
  config.saveGlobalConfig(current => { const next = { ...current }; delete next.companion; delete next.companionMuted; delete next.oauthAccount; delete next.userID; return { ...next, ...row.global } })
  const events: any[] = []
  let appState: any = {}
  let nowIndex = 0, randomIndex = 0
  const originalNow = Date.now, originalRandom = Math.random
  Date.now = () => row.now[Math.min(nowIndex++, row.now.length - 1)]
  Math.random = () => row.random[Math.min(randomIndex++, row.random.length - 1)]
  const stdin = new PassThrough() as any
  stdin.isTTY = true; stdin.setRawMode = () => {}; stdin.ref = () => stdin; stdin.unref = () => stdin
  const stdout = new PassThrough() as any
  stdout.isTTY = false; stdout.columns = row.width ?? 100
  let output = ''
  stdout.on('data', chunk => output += chunk.toString())
  const node = await call((...values: any[]) => events.push(values), { setAppState: (updater: any) => appState = updater(appState) } as any, row.args)
  const app = await render(React.createElement(ReplInputPump, null, node), { stdin, stdout, stderr: stdout, patchConsole: false, exitOnCtrlC: false })
  const settle = async () => { for (let i = 0; i < 10; i++) await new Promise(resolve => setImmediate(resolve)); await new Promise(resolve => setTimeout(resolve, 25)) }
  await settle()
  const frames = [{ key: 'mount', output, events: structuredClone(events) }]
  for (const key of row.keys ?? []) { output = ''; stdin.write(key); if (key === '\x1b') await new Promise(resolve => setTimeout(resolve, 120)); await settle(); frames.push({ key, output, events: structuredClone(events) }) }
  let layout
  if (input.layout) { const visit = (node: any): any => ({name:node.nodeName,text:node.nodeValue,layout:node.yogaNode ? {width:node.yogaNode.getComputedWidth(),height:node.yogaNode.getComputedHeight(),left:node.yogaNode.getComputedLeft(),top:node.yogaNode.getComputedTop()} : undefined,children:node.childNodes?.map(visit)}); layout=visit(instances.get(stdout)!.rootNode) }
  app.unmount(); await settle()
  Date.now = originalNow; Math.random = originalRandom
  const value = config.getGlobalConfig()
  results.push({ events, frames, appState, layout, global: { companion: value.companion, companionMuted: value.companionMuted, futureBuddy: value.futureBuddy } })
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Buddy card Source escaped private fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ declaration: { name: declaration.name, aliases: declaration.aliases, type: declaration.type }, rows: results }), error => error ? reject(error) : resolve()))
process.exit(0)
