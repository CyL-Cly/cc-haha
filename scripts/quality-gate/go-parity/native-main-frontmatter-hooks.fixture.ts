import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual main frontmatter hook registration')
if (process.env.CC_HAHA_MAIN_FRONTMATTER_FIXTURE !== '1') throw new Error('Missing main hook fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { registerFrontmatterHooks } = await import('../../../src/utils/hooks/registerFrontmatterHooks')
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const observations = []
for (const row of input.rows) {
  let state = getDefaultAppState()
  for (const hooks of row.registrations) registerFrontmatterHooks((fn: any) => { state = fn(state) }, row.sid, hooks, 'actual main', false)
  const store = state.sessionHooks.get(row.sid)
  const declarations = Object.fromEntries(Object.entries(store?.hooks ?? {}).map(([event, matchers]: any) => [event, matchers.map((matcher: any) => ({ matcher: matcher.matcher, hooks: matcher.hooks.map((entry: any) => entry.hook) }))]))
  observations.push(declarations)
}
process.stdout.write(JSON.stringify({ observations, capabilities }))
