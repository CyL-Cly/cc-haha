import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { PassThrough } from 'node:stream'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual companion sprite mounted animation, pet and narrow face')
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
const { render } = await import('../../../src/ink')
const instances = (await import('../../../src/ink/instances')).default
const { charInCellAt } = await import('../../../src/ink/screen')
const { AppStateProvider, getDefaultAppState } = await import('../../../src/state/AppState')
const { CompanionSprite, companionReservedColumns } = await import('../../../src/buddy/CompanionSprite')
const originalInterval = globalThis.setInterval, originalClear = globalThis.clearInterval
const intervals = new Map<number, { fn: any; args: any[] }>()
let nextInterval = 400000
globalThis.setInterval = ((fn: any, delay: number, ...args: any[]) => { if (delay !== 500) return originalInterval(fn, delay, ...args); const id = ++nextInterval; intervals.set(id, { fn, args }); return id }) as any
globalThis.clearInterval = ((id: any) => { if (!intervals.delete(id)) originalClear(id) }) as any
const results = []
for (const row of input.rows) {
  config.saveGlobalConfig(current => { const next = { ...current }; delete next.companion; delete next.companionMuted; return { ...next, ...row.global } })
  const stdin = new PassThrough() as any
  stdin.isTTY = true; stdin.setRawMode = () => {}; stdin.ref = () => stdin; stdin.unref = () => stdin
  const stdout = new PassThrough() as any
  stdout.isTTY = false; stdout.columns = row.width
  let output = ''
  stdout.on('data', chunk => output += chunk.toString())
  const initialState = { ...getDefaultAppState(), companionPetAt: row.petAt, footerSelection: row.focused ? 'companion' : null }
  const node = React.createElement(AppStateProvider, { initialState }, React.createElement(CompanionSprite))
  const app = await render(node, { stdin, stdout, stderr: stdout, patchConsole: false, exitOnCtrlC: false })
  const settle = async () => { for (let i = 0; i < 10; i++) await new Promise(resolve => setImmediate(resolve)); await new Promise(resolve => setTimeout(resolve, 25)) }
  await settle()
  const screenText = () => { const screen=(instances.get(stdout) as any).frontFrame.screen; const rows=[]; for(let y=0;y<screen.height;y++){let line='';for(let x=0;x<screen.width;x++)line+=charInCellAt(screen,x,y)??' ';rows.push(line.trimEnd())}return rows.join('\n') }
  const frames = [{ tick: 0, output, text: screenText() }]
  for (let tick = 1; tick <= 15; tick++) { output = ''; for (const timer of intervals.values()) timer.fn(...timer.args); await settle(); frames.push({ tick, output, text: screenText() }) }
  const reserved = companionReservedColumns(row.width, false)
  app.unmount(); await settle()
  if (intervals.size) throw new Error('Actual Source sprite interval not unmounted')
  results.push({ frames, reserved })
}
globalThis.setInterval = originalInterval; globalThis.clearInterval = originalClear
if (caps.blocked.length || caps.secureReads.length) throw new Error('Buddy sprite Source escaped private fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(results), error => error ? reject(error) : resolve()))
process.exit(0)
