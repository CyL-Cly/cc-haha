import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = await Bun.stdin.json()
if (Bun.version !== '1.3.14') throw new Error('Expected pinned Bun 1.3.14')
const home = assertDisposableFixtureHome(input.homeDir, 'Source frontmatter callback grouping')
if (process.env.CC_HAHA_FRONTMATTER_GROUP_SOURCE !== '1') throw new Error('Missing owned group fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { registerSkillHooks } = await import('../../../src/utils/hooks/registerSkillHooks')
const { registerFrontmatterHooks } = await import('../../../src/utils/hooks/registerFrontmatterHooks')
const { addSessionHook, getSessionHookCallback, getSessionHooks } = await import('../../../src/utils/hooks/sessionHooks')
const rows = []
for (const row of input.rows) {
  let state = getDefaultAppState(), called = 0
  const set = (fn: any) => { state = fn(state) }
  for (const registration of row.registrations) {
    if (registration.kind === 'skill') registerSkillHooks(set, 'hook-main', registration.hooks, 'actual-skill', registration.root)
    else if (registration.kind === 'main') registerFrontmatterHooks(set, 'hook-main', registration.hooks, 'actual-main', false)
    else if (registration.kind === 'goal-callback') addSessionHook(set, 'hook-main', row.event, registration.matcher, registration.hook, () => { called++ })
    else throw new Error('Undeclared group fixture registration')
  }
  const snapshot = () => Object.fromEntries(getSessionHooks(state, 'hook-main'))
  const count = () => [...getSessionHooks(state, 'hook-main').values()].reduce((sum: number, groups: any) => sum + groups.reduce((n: number, group: any) => n + group.hooks.length, 0), 0)
  const before = snapshot(), beforeCount = count()
  const selected = getSessionHookCallback(state, 'hook-main', row.event, row.matchQuery, row.completedHook)
  // Registration/callback-selector component evidence. The separate physical
  // fixture executes the original complete hook generator and real native shell.
  selected?.onHookSuccess?.(row.completedHook, {} as any)
  rows.push({ name: row.name, before, after: snapshot(), beforeCount, afterCount: count(), firstCallbackPresent: !!selected?.onHookSuccess, called })
}
if (capabilities.blocked.length) throw new Error('Source group fixture escaped owned boundary')
process.stdout.write(JSON.stringify({ rows, capabilities }))
