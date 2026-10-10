import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.homeDir, 'Native text prompt')
if (process.env.CC_HAHA_NATIVE_TEXT_PROMPT_FIXTURE !== '1') throw new Error('Unowned text prompt fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { processTextPrompt } = await import('../../../src/utils/processUserInput/processTextPrompt')
const rows = input.rows.map((row: any) => {
  const result = processTextPrompt(JSON.parse(row.content), [], [], [], row.uuid, row.mode)
  return { shouldQuery: result.shouldQuery, messages: result.messages.map((m: any) => { const { timestamp, ...rest } = m; return rest }) }
})
process.stdout.write(JSON.stringify({ rows, capabilities }))
