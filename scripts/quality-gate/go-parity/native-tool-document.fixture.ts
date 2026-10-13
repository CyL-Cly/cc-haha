import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Native tool document')
if (process.env.CC_HAHA_TOOL_DOCUMENT_FIXTURE !== '1') throw new Error('Unowned tool document fixture')
await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { normalizeMessagesForAPI } = await import('../../../src/utils/messages')
const results = input.rows.map((row: any) => normalizeMessagesForAPI([row], [], 'fixture-model'))
process.stdout.write(JSON.stringify(results))
