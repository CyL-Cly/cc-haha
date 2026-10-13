import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual bounded search projection admission')
if (process.env.CC_HAHA_SEARCH_BUDGET_FIXTURE !== '1') throw new Error('Missing search budget fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { withSearchProjectionBudget } = await import('../../../src/server/services/localIndex/searchContentCommitWorker')
const events: string[] = []
let release!: () => void
const active = withSearchProjectionBudget(undefined, async () => { events.push('first'); await new Promise<void>(resolve => { release = resolve }) })
const controller = new AbortController()
const cancelled = withSearchProjectionBudget(controller.signal, async () => { throw new Error('Cancelled waiter entered') }).then(() => 'incorrect', error => error.name === 'AbortError' ? 'cancelled' : String(error))
controller.abort()
const cancelledResult = await cancelled
const pending = Array.from({ length: 8 }, (_, i) => withSearchProjectionBudget(undefined, async () => { events.push(`queued-${i}`) }))
const overflow = await withSearchProjectionBudget(undefined, async () => { throw new Error('Overflow entered') }).then(() => 'incorrect', error => error.message)
const before = [...events]
release(); await active; await Promise.all(pending)
await withSearchProjectionBudget(undefined, async () => { events.push('after') })
process.stdout.write(JSON.stringify({ result: { cancelled: cancelledResult, overflow, before, events }, capabilities }))
process.exit(0)
