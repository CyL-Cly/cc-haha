import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Partial output')
if (process.env.CC_HAHA_NATIVE_PARTIAL_OUTPUT_FIXTURE !== '1') throw new Error('Unowned partial output fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { PrintPartialOutputTracker } = await import('../../../src/cli/partialOutput')
const rows = input.rows.map((row: { messages: Parameters<InstanceType<typeof PrintPartialOutputTracker>['observe']>[0][], result: string, error: boolean }) => {
  const tracker = new PrintPartialOutputTracker()
  for (const message of row.messages) tracker.observe(message)
  return { result: tracker.formatResult(row.result, row.error), line: tracker.formatResultLine(row.result, row.error) }
})
process.stdout.write(JSON.stringify({ rows, capabilities }))
