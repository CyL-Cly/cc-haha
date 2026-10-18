import fs from 'node:fs'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual inactive CU finally reactions')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_CU_FINALLY_REACTION !== '1') throw new Error('Pinned original CU finally profile required')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { cleanupComputerUseAfterTurn } = await import('../../../src/utils/computerUse/cleanup')
const rows = []
for (const profile of ['fulfilled', 'rejected', 'throwing']) {
  let tick = 0
  let completed = false
  const events: Array<{ kind: string; tick: number }> = []
  const original = new Error('original cleanup rejection')
  const thrown = new Error('finally callback failure')
  const parent = profile === 'rejected' ? Promise.reject(original) : Promise.resolve()
  // This is the actual frozen runtime's native Promise.finally. The two CU
  // callbacks return undefined: overlay retirement and clearTimeout. Observe
  // their reaction without replacing a Promise or its returned identity.
  const result = parent.finally(() => {
    events.push({ kind: 'finally', tick })
    if (profile === 'throwing') throw thrown
  })
  let completionTick = -1
  let sameError = false
  result.then(() => {
    events.push({ kind: 'completed', tick })
    completionTick = tick
    completed = true
  }, error => {
    events.push({ kind: 'completed', tick })
    completionTick = tick
    sameError = error === (profile === 'throwing' ? thrown : original)
    completed = true
  })
  const probe = () => {
    tick++
    events.push({ kind: 'probe', tick })
    if (!completed && tick < 32) queueMicrotask(probe)
  }
  queueMicrotask(probe)
  await result.catch(() => {})
  rows.push({ profile, completionTick, sameError, events: [...events] })
}
let reads = 0
const timers: ReturnType<typeof setTimeout>[] = []
const cleared = new Set<ReturnType<typeof setTimeout>>()
const timeout = globalThis.setTimeout, clear = globalThis.clearTimeout
globalThis.setTimeout = ((callback: any, delay?: number, ...args: any[]) => {
  const timer = timeout(callback, delay, ...args)
  if (delay === 2000) timers.push(timer)
  return timer
}) as typeof setTimeout
globalThis.clearTimeout = ((timer: any) => { cleared.add(timer); return clear(timer) }) as typeof clearTimeout
try {
  // Execute the complete original cleanup consumer with its shipped unit-test
  // dependency seam. Neither overlay nor badge can start a native daemon.
  await cleanupComputerUseAfterTurn({
    getAppState: () => { reads++; return {} as any },
    setAppState: () => { throw new Error('Inactive cleanup changed app state') },
  }, { overlayHide: () => Promise.resolve(), hideCursorBadge: () => {} })
} finally {
  globalThis.setTimeout = timeout
  globalThis.clearTimeout = clear
}
if (capabilities.blocked.length) throw new Error('CU finally escaped private capabilities: ' + JSON.stringify(capabilities.blocked))
fs.writeSync(1, JSON.stringify({ rows, fullCleanup: { reads, timers: timers.length, cleared: timers.filter(timer => cleared.has(timer)).length }, capabilities }))
process.exit(0)
