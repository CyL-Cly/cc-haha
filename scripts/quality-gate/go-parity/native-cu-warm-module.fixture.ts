import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual warm Computer Use module reactions')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_CU_WARM_MODULE !== '1') throw new Error('Pinned warm module guard required')
const absent = absentManagedSettingsFixturePaths()
for (const spelling of new Set([home, fs.realpathSync(home)])) {
  for (let path = spelling; ; path = dirname(path)) {
    const candidate = join(path, '.git')
    if (!fs.existsSync(candidate)) absent.push(candidate)
    if (dirname(path) === path) break
  }
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: '999.0.0-cu-warm', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const targets = [
  new URL('../../../src/utils/computerUse/cleanup.ts', import.meta.url).href,
  new URL('../../../src/utils/computerUse/cuHelperDaemon.ts', import.meta.url).href,
  new URL('../../../src/utils/computerUse/winCursorBadge.ts', import.meta.url).href,
]
// This profile measures the actual warmed runtime path separately from cold
// product-entry controls. No production module or Promise is replaced.
const modules = []
for (const target of targets) modules.push(await import(target))
const { parse } = await import('acorn')
const text = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/query/stopHooks.ts', import.meta.url)).text())
let branch: any
function walk(node: any) {
  if (!node || typeof node !== 'object') return
  if (node.type === 'IfStatement' && node.test?.type === 'UnaryExpression' && node.test.operator === '!' && node.test.argument?.property?.name === 'agentId' && text.slice(node.start, node.end).includes('cleanupComputerUseAfterTurn')) branch = node
  for (const value of Object.values(node)) {
    if (Array.isArray(value)) value.forEach(walk)
    else if (value && typeof value === 'object') walk(value)
  }
}
walk(parse(text, { ecmaVersion: 'latest', sourceType: 'module' }))
if (!branch) throw new Error('Original stop cleanup caller missing')
const dependency = branch.consequent.body[0].block.body[0].declarations[0].init
if (dependency.type !== 'AwaitExpression' || dependency.argument.type !== 'ImportExpression') throw new Error('Original cleanup import changed')
const literal = dependency.argument.source
const callerBody = text.slice(branch.start, literal.start) + JSON.stringify(targets[0]) + text.slice(literal.end, branch.end)
const caller = new Function('toolUseContext', 'return (async()=>{' + callerBody + '})()')
const rows = []
const context = { getAppState: () => ({}), setAppState: () => { throw new Error('Inactive cleanup cannot write application state') } }
for (const [profile, begin] of [
  ...targets.map((target, index) => ['module-' + index, () => import(target)] as const),
  ['cleanup', () => modules[0].cleanupComputerUseAfterTurn(context)],
  ['default-caller', () => caller(context)],
] as const) {
  let tick = 0, completed = false, completionTick = -1
  const pending = begin()
  const originalIdentity = Promise.resolve(pending) === pending
  pending.then(() => { completionTick = tick; completed = true })
  const probe = () => { tick++; if (!completed && tick < 256) queueMicrotask(probe) }
  queueMicrotask(probe)
  const namespace = await pending
  rows.push({ profile, completionTick, originalIdentity, sameNamespace: profile.startsWith('module-') ? namespace === modules[Number(profile.slice(7))] : undefined })
}
const one = import(targets[0]), two = import(targets[0])
const [first, second] = await Promise.all([one, two])
if (caps.blocked.length) throw new Error(JSON.stringify(caps.blocked))
process.stdout.write(JSON.stringify({ rows, distinctImportPromises: one !== two, sameNamespace: first === second, inactive: !modules[1].isOverlayShown() && !modules[2].__cursorBadgeIsRunning(), capabilities: caps }))
