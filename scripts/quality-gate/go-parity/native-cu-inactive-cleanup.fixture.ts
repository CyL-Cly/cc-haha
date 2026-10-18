import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual inactive Computer Use query cleanup')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_CU_INACTIVE_CLEANUP !== '1') throw new Error('Pinned inactive cleanup guard required')
const absent = absentManagedSettingsFixturePaths()
for (const spelling of new Set([home, fs.realpathSync(home)])) {
  for (let path = spelling; ; path = dirname(path)) {
    const candidate = join(path, '.git')
    if (!fs.existsSync(candidate)) absent.push(candidate)
    if (dirname(path) === path) break
  }
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: '999.0.0-cu-inactive', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const { parse } = await import('acorn')
let source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/query.ts', import.meta.url)).text())
const branches: any[] = []
function walk(node: any) {
  if (!node || typeof node !== 'object') return
  if (node.type === 'IfStatement' && node.test?.type === 'UnaryExpression' && node.test.operator === '!' && node.test.argument?.property?.name === 'agentId' && source.slice(node.start, node.end).includes('cleanupComputerUseAfterTurn')) branches.push({ ...node, original: source, kind: branches.length < 2 ? 'abort' : 'stop' })
  for (const value of Object.values(node)) {
    if (Array.isArray(value)) value.forEach(walk)
    else if (value && typeof value === 'object') walk(value)
  }
}
walk(astRoot())
function astRoot() { return parse(source, { ecmaVersion: 'latest', sourceType: 'module' }) }
if (branches.length !== 2) throw new Error('Original two abort cleanup callers changed')
source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/query/stopHooks.ts', import.meta.url)).text())
walk(astRoot())
if (branches.length !== 3) throw new Error('Original normal-stop cleanup caller changed')
const rows = []
for (let index = 0; index < branches.length; index++) {
  const branch = branches[index]
  const dependency = branch.consequent.body[0].block.body[0].declarations[0].init
  if (dependency.type !== 'AwaitExpression' || dependency.argument.type !== 'ImportExpression') throw new Error('Original cleanup import shape changed')
  const expression = dependency.argument
  const body = branch.original.slice(branch.start, expression.start) + 'importCleanup()' + branch.original.slice(expression.end, branch.end)
  const execute = new Function('toolUseContext', 'importCleanup', `return (async()=>{${body}})()`)
  const phases: string[] = []
  const timeouts: ReturnType<typeof setTimeout>[] = []
  const cleared = new Set<ReturnType<typeof setTimeout>>()
  const timeout = globalThis.setTimeout, clear = globalThis.clearTimeout
  globalThis.setTimeout = ((callback: any, delay?: number, ...args: any[]) => {
    const id = timeout(callback, delay, ...args)
    if (delay === 2000) timeouts.push(id)
    return id
  }) as typeof setTimeout
  globalThis.clearTimeout = ((id: any) => { cleared.add(id); return clear(id) }) as typeof clearTimeout
  const controller = new AbortController()
  if (input.cancelled) controller.abort('interrupt')
  let reads = 0
  const context = { agentId: input.child ? 'child-agent' : undefined, abortController: controller, getAppState: () => { reads++; phases.push('get-state'); return {} }, setAppState: () => { throw new Error('Inactive cleanup cannot write state') } }
  phases.push('start')
  const pending = execute(context, async () => {
    phases.push('import-start')
    const cleanup = await import('../../../src/utils/computerUse/cleanup')
    phases.push('import-ready')
    return cleanup
  })
  phases.push('returned-promise')
  await pending
  phases.push('completed')
  globalThis.setTimeout = timeout
  globalThis.clearTimeout = clear
  rows.push({ branch: index, kind: branch.kind, phases, stateReads: reads, timers: timeouts.length, timersCleared: timeouts.filter(id => cleared.has(id)).length, cancelled: controller.signal.aborted })
}
const daemon = await import('../../../src/utils/computerUse/cuHelperDaemon')
const lock = await import('../../../src/utils/computerUse/computerUseLock')
const badge = await import('../../../src/utils/computerUse/winCursorBadge')
const daemonSource = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/computerUse/cuHelperDaemon.ts', import.meta.url)).text())
const names = new Set(['needsOverlayReconciliation', 'reconcileOverlay', 'scheduleOverlayReconciliation', 'overlayHide'])
const functions: string[] = []
for (const node of (parse(daemonSource, { ecmaVersion: 'latest', sourceType: 'module' }) as any).body) {
  const declaration = node.type === 'ExportNamedDeclaration' ? node.declaration : node
  if (declaration?.type === 'FunctionDeclaration' && names.has(declaration.id?.name)) functions.push(daemonSource.slice(declaration.start, declaration.end))
}
if (functions.length !== names.size) throw new Error('Original overlay reconciliation functions changed')
const createOverlay = new Function(`let overlayDesiredVisible=false,overlayActualVisible=false,overlayCleanupRequested=false,overlayRevision=0,overlayReconcilePromise,activeTurnId,statePromise,activeDaemonGeneration,overlayActualKey;
let nativeCalls=0;const callDaemon=()=>{nativeCalls++;throw Error('Never-started overlay launched daemon')},callExistingDaemon=callDaemon,resetState=callDaemon,logForDebugging=callDaemon;
${functions.join('\n')}
return {hide:overlayHide,snapshot:()=>({revision:overlayRevision,cleanupRequested:overlayCleanupRequested,pending:overlayReconcilePromise!==undefined,nativeCalls})}`)
const overlay = createOverlay()
const one = overlay.hide(), first = overlay.snapshot(), two = overlay.hide(), second = overlay.snapshot()
const samePromise = one === two
await Promise.all([one,two])
const settled = overlay.snapshot()
const three = overlay.hide(), third = overlay.snapshot()
const newPromise = three !== one
await three
const resettled = overlay.snapshot()
if (caps.blocked.length) throw new Error('Inactive cleanup escaped private fixture: ' + JSON.stringify(caps.blocked))
process.stdout.write(JSON.stringify({ rows, inactive: !daemon.isOverlayShown() && !lock.isLockHeldLocally() && !badge.__cursorBadgeIsRunning(), capabilities: caps, overlay: { first, second, settled, third, resettled, samePromise, newPromise } }))
process.exit(0)
